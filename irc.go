package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/irc"
	"github.com/curedbylethe/booxd/internal/source"
	sourceirc "github.com/curedbylethe/booxd/internal/source/irc"
)

// ircSearcher is the slice of the source the CLI uses. Fetch is present for
// the get/show path; search only calls Search.
type ircSearcher interface {
	Search(ctx context.Context, query string) ([]sourceirc.Book, error)
	Fetch(ctx context.Context, book sourceirc.Book, dstPath string) (int64, error)
}

// newIRCSearcher dials the configured server. A variable so tests can swap it.
var newIRCSearcher = dialIRC

func dialIRC(ctx context.Context, cfg config.Config, log io.Writer) (ircSearcher, func() error, error) {
	host, port, err := splitServer(cfg.IRCServer)
	if err != nil {
		return nil, nil, err
	}
	ircCfg := irc.DefaultConfig()
	ircCfg.Host = host
	ircCfg.Port = port
	ircCfg.Nick = cfg.IRCNick
	ircCfg.Channel = cfg.IRCChannel
	// DefaultConfig already enables Trace for BOOXD_IRC_DEBUG; --verbose adds to it.
	if log != nil {
		ircCfg.Trace = log
	}
	client, err := irc.Dial(ctx, ircCfg)
	if err != nil {
		return nil, nil, err
	}
	return sourceirc.New(client), client.Close, nil
}

func splitServer(addr string) (host string, port int, err error) {
	host, raw, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("irc server must be host:port")
	}
	port, err = strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("irc server port must be 1-65535")
	}
	return host, port, nil
}

func searchIRC(cfg config.Config, query string, log, stdout, stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "irc: searching %s (experimental source)\n", cfg.IRCChannel)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.IRCSearchTimeout)
	defer cancel()
	s, cleanup, err := newIRCSearcher(ctx, cfg, log)
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	books, err := s.Search(ctx, query)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	if len(books) == 0 {
		_, _ = fmt.Fprintln(stderr, "no results")
		return 0
	}
	providers := make([]source.Book, len(books))
	for i, b := range books {
		providers[i] = b.Source()
	}
	printBooks(stdout, toParseBooks(providers))
	return 0
}
