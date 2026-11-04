package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/mirror"
	"github.com/curedbylethe/booxd/internal/parse"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, resolveMirror, newFetchClient))
}

type resolveFunc func(ctx context.Context, override string, client *fetch.Client) (string, error)
type clientFunc func(verbose io.Writer) *fetch.Client

func newFetchClient(verbose io.Writer) *fetch.Client {
	return fetch.New(fetch.WithVerbose(verbose))
}

func resolveMirror(ctx context.Context, override string, client *fetch.Client) (string, error) {
	if override != "" {
		return strings.TrimRight(override, "/"), nil
	}
	path, err := mirror.StatePath()
	if err != nil {
		return "", err
	}
	return mirror.Resolve(ctx, client, path, mirror.Seeds(), time.Now())
}

func run(args []string, stdout, stderr io.Writer, resolve resolveFunc, newClient clientFunc) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: booxd <search|get|show|tui|serve>")
		return 2
	}

	switch args[0] {
	case "search":
		flags := flag.NewFlagSet("search", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.Usage = func() {
			_, _ = fmt.Fprintln(stderr, "usage: booxd search [flags] <query>\nflags must precede query arguments")
			flags.PrintDefaults()
		}
		override := flags.String("mirror", "", "base URL; skip mirror discovery")
		page := flags.Int("page", 1, "search result page")
		dump := flags.String("dump-html", "", "write the fetched page to this path")
		verbose := flags.Bool("verbose", false, "log each fetch attempt to stderr")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		query := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if query == "" {
			_, _ = fmt.Fprintln(stderr, "usage: booxd search [flags] <query>")
			return 2
		}
		if *page < 1 {
			_, _ = fmt.Fprintln(stderr, "booxd: page must be at least 1")
			return 2
		}
		var log io.Writer
		if *verbose {
			log = stderr
		}
		client := newClient(log)
		ctx := context.Background()
		base, err := resolve(ctx, *override, client)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "mirror: %s\n", base)
		searchURL := base + "/search?q=" + url.QueryEscape(query)
		if *page > 1 {
			searchURL += "&page=" + strconv.Itoa(*page)
		}
		resp, err := client.Get(ctx, searchURL)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		if *dump != "" {
			if err := os.WriteFile(*dump, resp.Body, 0o644); err != nil {
				_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
				return 1
			}
		}
		if resp.StatusCode != 200 {
			_, _ = fmt.Fprintf(stderr, "booxd: search: status %d\n", resp.StatusCode)
			return 1
		}
		books, err := parse.Search(bytes.NewReader(resp.Body))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		if len(books) == 0 {
			_, _ = fmt.Fprintln(stderr, "no results")
			return 0
		}
		for _, b := range books {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.Title, b.Author, b.Year, b.Format, b.Size)
		}
		return 0
	case "get", "show", "tui", "serve":
		_, _ = fmt.Fprintf(stdout, "booxd: %s is not implemented yet\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "booxd: unknown command %q\n", args[0])
		return 2
	}
}
