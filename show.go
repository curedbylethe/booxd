package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/parse"
)

var bookIDPattern = regexp.MustCompile(`^[0-9]+(?:-[A-Za-z0-9._-]+)?$`)

func runShow(args []string, stdout, stderr io.Writer, resolve resolveFunc, newClient clientFunc) int {
	flags := flag.NewFlagSet("show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: booxd show [flags] <id>\nflags must precede the id")
		flags.PrintDefaults()
	}
	override := flags.String("mirror", "", "base URL; skip mirror discovery")
	configPath := flags.String("config", "", "config file path")
	verbose := flags.Bool("verbose", false, "log each fetch attempt to stderr")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 1 {
		flags.Usage()
		return 2
	}
	bookID := flags.Arg(0)
	if !bookIDPattern.MatchString(bookID) {
		_, _ = fmt.Fprintf(stderr, "booxd: invalid book id %q\n", bookID)
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
	detail, err := loadDetail(context.Background(), cfg, bookID, *override, store, resolve, newClient, log, stderr)
	if err != nil {
		var statusErr statusError
		if errors.As(err, &statusErr) {
			_, _ = fmt.Fprintf(stderr, "booxd: show: %s\n", statusErr.Error())
		} else {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		}
		return 1
	}
	renderShow(stdout, detail)
	return 0
}

// statusError reports a non-200 book page response.
type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("status %d", e.code) }

// loadDetail returns a book detail, reusing the cache before fetching. The
// cache key drops any slug suffix; the fetch URL keeps the ID as given. log
// receives fetch verbosity; mirror receives the resolved base URL.
func loadDetail(ctx context.Context, cfg config.Config, bookID, override string, store *cache.Store, resolve resolveFunc, newClient clientFunc, log, mirror io.Writer) (parse.Detail, error) {
	id, _, _ := strings.Cut(bookID, "-")
	now := time.Now()
	if store != nil {
		if entry, ok := store.Get("book:"+id, 0); ok && entry.Status == http.StatusOK {
			age := now.Sub(entry.FetchedAt)
			if age >= 0 && age < cache.DefaultTTL {
				if detail, parseErr := parse.DetailPage(bytes.NewReader(entry.Body)); parseErr == nil {
					return detail, nil
				}
			}
		}
	}

	client := newClient(cfg, log, nil)
	base, err := resolve(ctx, cfg, override, client)
	if err != nil {
		return parse.Detail{}, err
	}
	if mirror != nil {
		_, _ = fmt.Fprintf(mirror, "mirror: %s\n", base)
	}
	response, err := client.Get(ctx, strings.TrimRight(base, "/")+"/books/"+bookID)
	if err != nil {
		return parse.Detail{}, err
	}
	if response.StatusCode != http.StatusOK {
		return parse.Detail{}, statusError{code: response.StatusCode}
	}
	detail, err := parse.DetailPage(bytes.NewReader(response.Body))
	if err != nil {
		return parse.Detail{}, err
	}
	if store != nil {
		_ = store.Put(cache.Entry{Query: "book:" + id, Page: 0, Status: http.StatusOK, FetchedAt: now, Body: response.Body})
	}
	return detail, nil
}

func renderShow(w io.Writer, detail parse.Detail) {
	fields := []struct{ name, value string }{
		{"title", detail.Title},
		{"author", detail.Author},
		{"publisher", detail.Publisher},
		{"year", detail.Year},
		{"language", detail.Language},
		{"format", detail.Format},
		{"size", detail.Size},
		{"isbn", detail.ISBN},
		{"pages", detail.Pages},
	}
	for _, field := range fields {
		if field.value == "" {
			field.value = "-"
		}
		_, _ = fmt.Fprintf(w, "%s: %s\n", field.name, field.value)
	}
	switch {
	case detail.LoginWall:
		_, _ = fmt.Fprintln(w, "downloads require a signed-in session")
	case len(detail.Downloads) == 0:
		_, _ = fmt.Fprintln(w, "downloads: -")
	default:
		_, _ = fmt.Fprintln(w, "downloads:")
		for _, download := range detail.Downloads {
			label := download.Label
			if download.RequiresSession {
				label += " (requires signed-in session)"
			}
			_, _ = fmt.Fprintf(w, "  - %s\n", label)
		}
	}
}
