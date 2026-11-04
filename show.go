package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
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
	id, _, _ := strings.Cut(bookID, "-")

	cfg, err := loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	now := time.Now()
	var store *cache.Store
	if dir, pathErr := cache.Path(cfg.CacheDir); pathErr == nil {
		store = cache.Open(dir)
		_ = store.Sweep(cache.SweepAge)
	}
	if store != nil {
		if entry, ok := store.Get("book:"+id, 0); ok && entry.Status == http.StatusOK {
			age := now.Sub(entry.FetchedAt)
			if age >= 0 && age < cache.DefaultTTL {
				if detail, parseErr := parse.DetailPage(bytes.NewReader(entry.Body)); parseErr == nil {
					renderShow(stdout, detail)
					return 0
				}
			}
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
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "mirror: %s\n", base)
	response, err := client.Get(ctx, strings.TrimRight(base, "/")+"/books/"+bookID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	if response.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "booxd: show: status %d\n", response.StatusCode)
		return 1
	}
	detail, err := parse.DetailPage(bytes.NewReader(response.Body))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	if store != nil {
		_ = store.Put(cache.Entry{Query: "book:" + id, Page: 0, Status: http.StatusOK, FetchedAt: now, Body: response.Body})
	}
	renderShow(stdout, detail)
	return 0
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
