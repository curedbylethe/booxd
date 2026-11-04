package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/parse"
)

type searchOptions struct {
	Page    int
	MaxAge  time.Duration
	NoCache bool
}

type searchOutcome struct {
	Books  []parse.Book
	Body   []byte
	Cached bool
	Age    time.Duration
}

type searcher struct {
	cfg           config.Config
	resolve       resolveFunc
	client        *fetch.Client
	clientFactory func() *fetch.Client
	store         *cache.Store
	stderr        io.Writer
	now           func() time.Time
}

func newSearcher(cfg config.Config, client *fetch.Client, store *cache.Store, resolve resolveFunc, stderr io.Writer) *searcher {
	return &searcher{cfg: cfg, client: client, store: store, resolve: resolve, stderr: stderr, now: time.Now}
}

func (s *searcher) search(ctx context.Context, query string, opts searchOptions) (searchOutcome, error) {
	var cached cache.Entry
	hit := false
	if s.store != nil && !opts.NoCache {
		cached, hit = s.store.Get(query, opts.Page)
	}
	age := s.now().Sub(cached.FetchedAt)
	if hit && age >= 0 && age < opts.MaxAge {
		books, err := parse.Search(bytes.NewReader(cached.Body))
		if err != nil {
			return searchOutcome{Body: cached.Body}, err
		}
		return searchOutcome{Books: books, Body: cached.Body, Cached: true, Age: age}, nil
	}
	if s.client == nil && s.clientFactory != nil {
		s.client = s.clientFactory()
	}
	fallback := func(err error) (searchOutcome, error) {
		if !hit {
			return searchOutcome{}, err
		}
		books, parseErr := parse.Search(bytes.NewReader(cached.Body))
		if parseErr != nil {
			return searchOutcome{Body: cached.Body}, parseErr
		}
		return searchOutcome{Books: books, Body: cached.Body, Cached: true, Age: age}, err
	}
	base, err := s.resolve(ctx, s.cfg, "", s.client)
	if err != nil {
		return fallback(err)
	}
	if s.stderr != nil {
		_, _ = fmt.Fprintf(s.stderr, "mirror: %s\n", base)
	}
	searchURL := base + "/search?q=" + url.QueryEscape(query)
	if opts.Page > 1 {
		searchURL += "&page=" + strconv.Itoa(opts.Page)
	}
	resp, err := s.client.Get(ctx, searchURL)
	if err != nil {
		return fallback(err)
	}
	body := resp.Body
	if resp.StatusCode != 200 {
		if hit {
			return fallback(fmt.Errorf("search: status %d", resp.StatusCode))
		}
		return searchOutcome{Body: body}, fmt.Errorf("search: status %d", resp.StatusCode)
	}
	books, err := parse.Search(bytes.NewReader(body))
	if err != nil {
		return searchOutcome{Body: body}, err
	}
	if len(books) > 0 && s.store != nil {
		_ = s.store.Put(cache.Entry{Query: query, Page: opts.Page, Status: resp.StatusCode, FetchedAt: s.now(), Body: body})
	}
	return searchOutcome{Books: books, Body: body}, nil
}
