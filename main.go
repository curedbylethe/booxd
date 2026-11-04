package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/mirror"
	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/tui"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, resolveMirror, newFetchClient))
}

type resolveFunc func(ctx context.Context, cfg config.Config, override string, client *fetch.Client) (string, error)
type clientFunc func(cfg config.Config, verbose io.Writer) *fetch.Client

func newFetchClient(cfg config.Config, verbose io.Writer) *fetch.Client {
	return fetch.New(fetch.WithVerbose(verbose), fetch.WithUserAgent(cfg.UserAgent), fetch.WithTimeout(cfg.RequestTimeout), fetch.WithInterval(cfg.PerHostInterval), fetch.WithProxy(cfg.Proxy))
}

func resolveMirror(ctx context.Context, cfg config.Config, override string, client *fetch.Client) (string, error) {
	if override != "" {
		return strings.TrimRight(override, "/"), nil
	}
	path, err := mirror.StatePath(cfg.CacheDir)
	if err != nil {
		return "", err
	}
	return mirror.Resolve(ctx, client, path, cfg.Mirrors, time.Now())
}

func loadConfig(flagPath string) (config.Config, error) {
	base := config.Defaults()
	base.Mirrors = mirror.Seeds()
	path := flagPath
	explicit := path != ""
	if path == "" {
		path = os.Getenv("BOOXD_CONFIG")
		explicit = path != ""
	}
	if path == "" {
		path, _ = config.Path()
	}
	return config.Load(path, explicit, base)
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
		configPath := flags.String("config", "", "config file path")
		page := flags.Int("page", 1, "search result page")
		dump := flags.String("dump-html", "", "write the fetched page to this path")
		maxAge := flags.Duration("max-age", cache.DefaultTTL, "maximum age of cached search results")
		noCache := flags.Bool("no-cache", false, "skip cached search results")
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
		cfg, err := loadConfig(*configPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		var store *cache.Store
		if dir, pathErr := cache.Path(cfg.CacheDir); pathErr == nil {
			store = cache.Open(dir)
			_ = store.Sweep(cache.SweepAge)
		}
		var log io.Writer
		if *verbose {
			log = stderr
		}
		ctx := context.Background()
		resolveSearch := func(ctx context.Context, cfg config.Config, _ string, client *fetch.Client) (string, error) {
			return resolve(ctx, cfg, *override, client)
		}
		search := newSearcher(cfg, nil, store, resolveSearch, stderr)
		search.clientFactory = func() *fetch.Client { return newClient(cfg, log) }
		outcome, err := search.search(ctx, query, searchOptions{Page: *page, MaxAge: *maxAge, NoCache: *noCache})
		if *dump != "" && outcome.Body != nil {
			if err := os.WriteFile(*dump, outcome.Body, 0o644); err != nil {
				_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
				return 1
			}
		}
		if err != nil && !outcome.Cached {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		if outcome.Cached {
			printCached(stderr, outcome.Age)
		}
		if len(outcome.Books) == 0 {
			_, _ = fmt.Fprintln(stderr, "no results")
			return 0
		}
		printBooks(stdout, outcome.Books)
		return 0
	case "show":
		return runShow(args[1:], stdout, stderr, resolve, newClient)
	case "tui":
		flags := flag.NewFlagSet("tui", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.Usage = func() {
			_, _ = fmt.Fprintln(stderr, "usage: booxd tui [flags]")
			flags.PrintDefaults()
		}
		mono := flags.Bool("mono", false, "disable colour")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		cfg, err := loadConfig("")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		var store *cache.Store
		if dir, pathErr := cache.Path(cfg.CacheDir); pathErr == nil {
			store = cache.Open(dir)
		}
		client := newClient(cfg, nil)
		search := newSearcher(cfg, client, store, resolve, nil)
		searchFunc := func(ctx context.Context, query string) ([]parse.Book, error) {
			outcome, err := search.search(ctx, query, searchOptions{Page: 1, MaxAge: cache.DefaultTTL})
			return outcome.Books, err
		}
		if err := tui.Run(context.Background(), tui.Options{Search: searchFunc, Mono: monoEnabled(*mono, os.Getenv("NO_COLOR"))}); err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		return 0
	case "get", "serve":
		_, _ = fmt.Fprintf(stdout, "booxd: %s is not implemented yet\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "booxd: unknown command %q\n", args[0])
		return 2
	}
}

func monoEnabled(flagMono bool, noColor string) bool {
	return flagMono || noColor != ""
}

func printBooks(stdout io.Writer, books []parse.Book) {
	for _, b := range books {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.Title, b.Author, b.Year, b.Format, b.Size)
	}
}

func printCached(w io.Writer, age time.Duration) {
	if age < 0 {
		age = 0
	}
	var value time.Duration
	var unit string
	switch {
	case age >= 24*time.Hour:
		value, unit = age/(24*time.Hour), "d"
	case age >= time.Hour:
		value, unit = age/time.Hour, "h"
	case age >= time.Minute:
		value, unit = age/time.Minute, "m"
	default:
		value, unit = age/time.Second, "s"
	}
	_, _ = fmt.Fprintf(w, "cached %d%s ago\n", value, unit)
}
