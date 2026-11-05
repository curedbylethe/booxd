package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestSearchFreshCache(t *testing.T) {
	store := cache.Open(t.TempDir())
	now := time.Now()
	if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now.Add(-time.Hour), Body: []byte(card)}); err != nil {
		t.Fatal(err)
	}
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		t.Fatal("fresh cache fetched")
		return 0, nil, false
	}))
	search := newSearcher(config.Config{}, client, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		t.Fatal("fresh cache resolved mirror")
		return "", nil
	}, nil)
	search.now = func() time.Time { return now }

	outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: 24 * time.Hour})
	if err != nil || !outcome.Cached || outcome.Age != time.Hour || len(outcome.Books) != 1 || string(outcome.Body) != card {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
}

func TestSearchUnparseableCacheIsError(t *testing.T) {
	const body = "<p>Showing 5 results on this page</p>"
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{
		{"fresh", time.Minute},
		{"stale", 3 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cache.Open(t.TempDir())
			now := time.Now()
			if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now.Add(-tc.age), Body: []byte(body)}); err != nil {
				t.Fatal(err)
			}
			search := newSearcher(config.Config{}, nil, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
				return "", errors.New("offline")
			}, nil)
			search.now = func() time.Time { return now }
			outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
			if err == nil || outcome.Cached {
				t.Fatalf("outcome = %#v, error = %v", outcome, err)
			}
		})
	}
}

func TestSearchRefetchesUnparseableFreshCache(t *testing.T) {
	const body = "<p>Showing 5 results on this page</p>"
	for _, tc := range []struct {
		name        string
		resolveErr  error
		wantLookups int
	}{
		{name: "refetch succeeds", wantLookups: 1},
		{name: "refetch fails", resolveErr: errors.New("offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cache.Open(t.TempDir())
			now := time.Now()
			if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now.Add(-time.Minute), Body: []byte(body)}); err != nil {
				t.Fatal(err)
			}
			resolveCalls, lookupCalls := 0, 0
			client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
				lookupCalls++
				return 200, []byte(card), true
			}))
			search := newSearcher(config.Config{}, client, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
				resolveCalls++
				if tc.resolveErr != nil {
					return "", tc.resolveErr
				}
				return "https://mirror.test", nil
			}, nil)
			search.now = func() time.Time { return now }

			outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
			if tc.resolveErr != nil {
				if err == nil || err.Error() != "parse search: parsed 0 of 5 results" || outcome.Cached {
					t.Fatalf("outcome = %#v, error = %v", outcome, err)
				}
			} else {
				if err != nil || len(outcome.Books) != 1 || outcome.Cached {
					t.Fatalf("outcome = %#v, error = %v", outcome, err)
				}
				entry, ok := store.Get("test", 1)
				if !ok || string(entry.Body) != card || !entry.FetchedAt.Equal(now) {
					t.Fatalf("stored entry = %#v, exists = %v", entry, ok)
				}
			}
			if resolveCalls != 1 || lookupCalls != tc.wantLookups {
				t.Fatalf("resolve calls = %d, lookup calls = %d", resolveCalls, lookupCalls)
			}
		})
	}
}

func TestSearchStaleCacheFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "resolve error"},
		{name: "non-200", status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cache.Open(t.TempDir())
			now := time.Now()
			if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now.Add(-3 * time.Hour), Body: []byte(card)}); err != nil {
				t.Fatal(err)
			}
			client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
				return tc.status, []byte("replacement"), true
			}))
			resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
				if tc.name == "resolve error" {
					return "", errors.New("offline")
				}
				return "https://mirror.test", nil
			}
			search := newSearcher(config.Config{}, client, store, resolve, nil)
			search.now = func() time.Time { return now }
			outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
			if err == nil || !outcome.Cached || len(outcome.Books) != 1 || string(outcome.Body) != card || outcome.Age != 3*time.Hour {
				t.Fatalf("outcome = %#v, error = %v", outcome, err)
			}
			if tc.name == "resolve error" && err.Error() != "offline" {
				t.Fatalf("error = %v", err)
			}
			if tc.name == "non-200" && err.Error() != "search: status 500" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSearchNon200WithoutCache(t *testing.T) {
	const body = "unavailable"
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		return 503, []byte(body), true
	}))
	search := newSearcher(config.Config{}, client, nil, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}, nil)
	outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
	if err == nil || err.Error() != "search: status 503" || outcome.Cached || string(outcome.Body) != body {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
}

func TestSearchCachesSuccessfulResults(t *testing.T) {
	store := cache.Open(t.TempDir())
	now := time.Now()
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		return 200, []byte(card), true
	}))
	search := newSearcher(config.Config{}, client, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}, nil)
	search.now = func() time.Time { return now }
	outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
	if err != nil || outcome.Cached || len(outcome.Books) != 1 {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
	entry, ok := store.Get("test", 1)
	if !ok || entry.Status != 200 || !entry.FetchedAt.Equal(now) || string(entry.Body) != card {
		t.Fatalf("stored entry = %#v, exists = %v", entry, ok)
	}
}

func TestSearchParseErrorKeepsCache(t *testing.T) {
	store := cache.Open(t.TempDir())
	old := []byte(card)
	now := time.Now()
	if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now.Add(-3 * time.Hour), Body: old}); err != nil {
		t.Fatal(err)
	}
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		return 200, []byte("<div>Showing 2 results on this page</div>" + card), true
	}))
	search := newSearcher(config.Config{}, client, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}, nil)
	search.now = func() time.Time { return now }
	outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour})
	if err == nil || outcome.Cached || string(outcome.Body) != "<div>Showing 2 results on this page</div>"+card {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
	entry, ok := store.Get("test", 1)
	if !ok || string(entry.Body) != string(old) {
		t.Fatalf("cache changed: %#v, exists = %v", entry, ok)
	}
}

func TestSearchNoCacheSkipsReadAndStillWrites(t *testing.T) {
	store := cache.Open(t.TempDir())
	now := time.Now()
	if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: now, Body: []byte("old")}); err != nil {
		t.Fatal(err)
	}
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		return 200, []byte(card), true
	}))
	search := newSearcher(config.Config{}, client, store, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}, nil)
	search.now = func() time.Time { return now.Add(time.Minute) }
	outcome, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour, NoCache: true})
	if err != nil || outcome.Cached || len(outcome.Books) != 1 {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
	entry, ok := store.Get("test", 1)
	if !ok || string(entry.Body) != card {
		t.Fatalf("stored entry = %#v, exists = %v", entry, ok)
	}
}

func TestSearchWritesMirrorBeforeFetch(t *testing.T) {
	var stderr bytes.Buffer
	client := fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
		if stderr.String() != "mirror: https://mirror.test\n" {
			t.Errorf("stderr before fetch = %q", stderr.String())
		}
		return 200, []byte(card), true
	}))
	search := newSearcher(config.Config{}, client, nil, func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}, &stderr)
	if _, err := search.search(context.Background(), "test", searchOptions{Page: 1, MaxAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stderr.String(), "mirror: https://mirror.test\n") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
