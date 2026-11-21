package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	sourceirc "github.com/curedbylethe/booxd/internal/source/irc"
)

const (
	ircNotice = "irc: searching #ebooks (experimental source)\n"
	ircRow    = "irc:%21artemis_serv%2016d6770d2ba9%20%7C%20The%20Last%20Hero.pdf\tThe Last Hero\t\t\tpdf\t9.1 MB\n"
)

type fakeIRCSearcher struct {
	books []sourceirc.Book
	err   error
	calls int
}

func (f *fakeIRCSearcher) Search(context.Context, string) ([]sourceirc.Book, error) {
	f.calls++
	return f.books, f.err
}

func (f *fakeIRCSearcher) Fetch(context.Context, sourceirc.Book, string) (int64, error) {
	return 0, errors.New("unexpected Fetch")
}

type ircHook struct {
	searcher ircSearcher
	cleanup  func() error
	dialErr  error
	calls    int
}

// stubIRCSearcher swaps the dial hook for the duration of the test.
func stubIRCSearcher(t *testing.T, s ircSearcher, cleanup func() error, dialErr error) *ircHook {
	t.Helper()
	h := &ircHook{searcher: s, cleanup: cleanup, dialErr: dialErr}
	old := newIRCSearcher
	newIRCSearcher = func(context.Context, config.Config, io.Writer) (ircSearcher, func() error, error) {
		h.calls++
		return h.searcher, h.cleanup, h.dialErr
	}
	t.Cleanup(func() { newIRCSearcher = old })
	return h
}

func ircEnvironment(t *testing.T) {
	t.Helper()
	withGetEnvironment(t, t.TempDir())
	for _, key := range []string{
		"BOOXD_IRC_SERVER", "BOOXD_IRC_NICK", "BOOXD_IRC_CHANNEL",
		"BOOXD_IRC_SEARCH_TIMEOUT", "BOOXD_IRC_DOWNLOAD_TIMEOUT", "BOOXD_IRC_DEBUG",
	} {
		t.Setenv(key, "")
	}
}

func countingSeams() (resolveFunc, clientFunc, *int, *int) {
	resolves, clients := 0, 0
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		resolves++
		return "", errors.New("unexpected resolver call")
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
		clients++
		return nil
	}
	return resolve, newClient, &resolves, &clients
}

func ircTestBook() sourceirc.Book {
	return sourceirc.Book{Server: "artemis_serv", ID: "16d6770d2ba9", Name: "The Last Hero", Format: "pdf", Size: "9.1 MB"}
}

func TestSearchIRC(t *testing.T) {
	cfg := config.Config{IRCChannel: "#ebooks", IRCSearchTimeout: time.Second}
	for _, tt := range []struct {
		name               string
		books              []sourceirc.Book
		searchErr, dialErr error
		code, searchCalls  int
		out, err           string
	}{
		{
			name: "success", books: []sourceirc.Book{ircTestBook()},
			code: 0, searchCalls: 1, out: ircRow, err: ircNotice,
		},
		{
			name: "no results", code: 0, searchCalls: 1,
			err: ircNotice + "no results\n",
		},
		{
			name: "dial error", dialErr: errors.New("irc: dial failed"),
			code: 1, searchCalls: 0, err: ircNotice + "booxd: irc: dial failed\n",
		},
		{
			name: "search error", searchErr: errors.New("source irc: search \"q\": no offer"),
			code: 1, searchCalls: 1, err: ircNotice + "booxd: source irc: search \"q\": no offer\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleaned := 0
			f := &fakeIRCSearcher{books: tt.books, err: tt.searchErr}
			h := stubIRCSearcher(t, f, func() error { cleaned++; return nil }, tt.dialErr)
			var stdout, stderr bytes.Buffer
			code := searchIRC(cfg, "q", nil, &stdout, &stderr)
			if code != tt.code || stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout.String(), stderr.String(), tt.code, tt.out, tt.err)
			}
			if h.calls != 1 || f.calls != tt.searchCalls || cleaned != 1 {
				t.Fatalf("dial=%d search=%d cleanup=%d; want 1 %d 1", h.calls, f.calls, cleaned, tt.searchCalls)
			}
		})
	}
}

func TestSearchIRCFlags(t *testing.T) {
	for _, tt := range []struct {
		name     string
		flags    []string
		code     int
		out, err string
	}{
		{name: "mirror", flags: []string{"--mirror", "https://x"}, code: 2, err: "booxd: --mirror applies to anna only\n"},
		{name: "dump-html", flags: []string{"--dump-html", "dump.html"}, code: 2, err: "booxd: --dump-html applies to anna only\n"},
		{name: "max-age", flags: []string{"--max-age", "1s"}, code: 2, err: "booxd: --max-age applies to anna only\n"},
		{name: "page", flags: []string{"--page", "2"}, code: 2, err: "booxd: --page applies to anna and zlibrary only\n"},
		{name: "language", flags: []string{"--language", "en"}, code: 2, err: "booxd: --language applies to zlibrary only\n"},
		{name: "format", flags: []string{"--format", "pdf"}, code: 2, err: "booxd: --format applies to zlibrary only\n"},
		{name: "no-cache is a no-op", flags: []string{"--no-cache"}, code: 0, out: ircRow, err: ircNotice},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ircEnvironment(t)
			stubIRCSearcher(t, &fakeIRCSearcher{books: []sourceirc.Book{ircTestBook()}}, func() error { return nil }, nil)
			resolve, newClient, resolves, clients := countingSeams()
			args := append([]string{"search", "--source", "irc"}, tt.flags...)
			args = append(args, "q")
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != tt.code || stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout.String(), stderr.String(), tt.code, tt.out, tt.err)
			}
			if *resolves != 0 || *clients != 0 {
				t.Fatalf("resolver=%d client=%d; want 0 0", *resolves, *clients)
			}
		})
	}
}

func TestSplitServer(t *testing.T) {
	for _, tt := range []struct {
		addr, host string
		port       int
		ok         bool
	}{
		{addr: "irc.example.org:6697", host: "irc.example.org", port: 6697, ok: true},
		{addr: "irc.example.org"},
		{addr: "irc.example.org:chat"},
		{addr: "irc.example.org:0"},
		{addr: "irc.example.org:70000"},
	} {
		t.Run(tt.addr, func(t *testing.T) {
			host, port, err := splitServer(tt.addr)
			if tt.ok {
				if err != nil || host != tt.host || port != tt.port {
					t.Fatalf("splitServer(%q) = %q %d %v; want %q %d nil", tt.addr, host, port, err, tt.host, tt.port)
				}
				return
			}
			if err == nil {
				t.Fatalf("splitServer(%q) = %q %d nil; want an error", tt.addr, host, port)
			}
		})
	}
}

func TestSearchIRCDoesNotTouchHTTP(t *testing.T) {
	ircEnvironment(t)
	stubIRCSearcher(t, &fakeIRCSearcher{books: []sourceirc.Book{ircTestBook()}}, func() error { return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "--source", "irc", "dune"}, &stdout, &stderr, resolve, newClient)
	if code != 0 || stdout.String() != ircRow || stderr.String() != ircNotice {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if *resolves != 0 || *clients != 0 {
		t.Fatalf("resolver=%d client=%d; want 0 0", *resolves, *clients)
	}
}
