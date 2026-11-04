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

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/mirror"
	"github.com/curedbylethe/booxd/internal/parse"
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
		var cached cache.Entry
		hit := false
		serveCached := func() int {
			printCached(stderr, time.Since(cached.FetchedAt))
			if *dump != "" {
				if err := os.WriteFile(*dump, cached.Body, 0o644); err != nil {
					_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
					return 1
				}
			}
			code, _ := renderSearch(stdout, stderr, cached.Body)
			return code
		}
		if store != nil && !*noCache {
			cached, hit = store.Get(query, *page)
			age := time.Since(cached.FetchedAt)
			if hit && age >= 0 && age < *maxAge {
				return serveCached()
			}
		}
		var log io.Writer
		if *verbose {
			log = stderr
		}
		client := newClient(cfg, log)
		ctx := context.Background()
		base, err := resolve(ctx, cfg, *override, client)
		if err != nil {
			if hit {
				return serveCached()
			}
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
			if hit {
				return serveCached()
			}
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
			if hit {
				return serveCached()
			}
			_, _ = fmt.Fprintf(stderr, "booxd: search: status %d\n", resp.StatusCode)
			return 1
		}
		code, count := renderSearch(stdout, stderr, resp.Body)
		if code == 0 && count > 0 && store != nil {
			_ = store.Put(cache.Entry{Query: query, Page: *page, Status: resp.StatusCode, FetchedAt: time.Now(), Body: resp.Body})
		}
		return code
	case "get", "show", "tui", "serve":
		_, _ = fmt.Fprintf(stdout, "booxd: %s is not implemented yet\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "booxd: unknown command %q\n", args[0])
		return 2
	}
}

func renderSearch(stdout, stderr io.Writer, body []byte) (int, int) {
	books, err := parse.Search(bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1, 0
	}
	if len(books) == 0 {
		_, _ = fmt.Fprintln(stderr, "no results")
		return 0, 0
	}
	for _, b := range books {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.Title, b.Author, b.Year, b.Format, b.Size)
	}
	return 0, len(books)
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
