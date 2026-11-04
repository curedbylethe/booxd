package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
)

func showFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("internal", "parse", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRunShow(t *testing.T) {
	full := showFixture(t, "book-detail.html")
	rich := showFixture(t, "book-detail-rich.html")
	search := showFixture(t, "search-hit.html")
	fullOutput := "title: Test\nauthor: -\npublisher: -\nyear: 2022\nlanguage: English\nformat: PDF\nsize: 51.0 MB\nisbn: -\npages: 1\ndownloads require a signed-in session\n"
	richOutput := "title: Test\nauthor: Sleator, William\npublisher: Amulet Books\nyear: 2010\nlanguage: English\nformat: EPUB\nsize: 3.0 MB\nisbn: 9780810993563,9780810989894\npages: -\ndownloads require a signed-in session\n"
	usage := "usage: booxd show [flags] <id>\nflags must precede the id\n  -config string\n    \tconfig file path\n  -mirror string\n    \tbase URL; skip mirror discovery\n  -verbose\n    \tlog each fetch attempt to stderr\n"
	mixed := `<div id="book-panel-details"><table><tr><td>Title</td><td>Test</td></tr></table></div>` +
		`<div id="book-panel-downloads"><li class="list-disc"><span class="font-medium text-[#444]">Fast Partner Server #1</span><a href="https://annas-archive.is/account">Log in to access downloads</a></li>` +
		`<li class="list-disc"><span class="font-medium text-[#444]">Slow Partner Server #1</span><a href="https://annas-archive.is/slow_download/abc/0/0">Download</a></li></div>`
	missing := `<div id="book-panel-details"><table><tr><td>Title</td><td>One</td></tr></table></div>`
	missingOutput := "title: One\nauthor: -\npublisher: -\nyear: -\nlanguage: -\nformat: -\nsize: -\nisbn: -\npages: -\ndownloads: -\n"
	tests := []struct {
		name                         string
		args                         []string
		body                         []byte
		status                       int
		resolveErr                   error
		wantOut, wantErr, wantURL    string
		wantCode, wantClients, calls int
		seedAge                      time.Duration
		seedFuture                   bool
		seedBody                     []byte
		seedStatus                   int
		wantNoEntry                  bool
		wantStoredBody               []byte
		wantLookups                  int
		verbose                      bool
	}{
		{name: "no id", args: []string{"show"}, wantCode: 2, wantErr: usage},
		{name: "two ids", args: []string{"show", "1", "2"}, wantCode: 2, wantErr: usage},
		{name: "invalid id", args: []string{"show", "../etc"}, wantCode: 2, wantErr: `booxd: invalid book id "../etc"` + "\n"},
		{name: "help", args: []string{"show", "-h"}, wantCode: 2, wantErr: usage},
		{name: "full fixture", args: []string{"show", "36451"}, body: full, status: 200, wantCode: 0, wantOut: fullOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, wantLookups: 1},
		{name: "slug id", args: []string{"show", "36451-36451-test"}, body: full, status: 200, wantCode: 0, wantOut: fullOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/36451-36451-test", wantClients: 1, calls: 1},
		{name: "rich fixture", args: []string{"show", "30208739"}, body: rich, status: 200, wantCode: 0, wantOut: richOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/30208739", wantClients: 1, calls: 1},
		{name: "404", args: []string{"show", "36451"}, status: 404, wantCode: 1, wantErr: "mirror: https://mirror.test\nbooxd: show: status 404\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, wantLookups: 1, wantNoEntry: true},
		{name: "resolver error", args: []string{"show", "36451"}, resolveErr: errors.New("no working mirror: x"), wantCode: 1, wantErr: "booxd: no working mirror: x\n", wantClients: 1, calls: 1},
		{name: "parse error", args: []string{"show", "36451"}, body: search, status: 200, wantCode: 1, wantErr: "mirror: https://mirror.test\nbooxd: parse detail: no detail table\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, wantLookups: 1, wantNoEntry: true},
		{name: "fresh cache", args: []string{"show", "36451"}, wantCode: 0, wantOut: fullOutput, seedBody: full, seedStatus: 200},
		{name: "stale cache", args: []string{"show", "36451"}, body: full, status: 200, wantCode: 0, wantOut: fullOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, seedAge: 25 * time.Hour, seedBody: full, seedStatus: 200, wantLookups: 1},
		{name: "unparseable cache", args: []string{"show", "36451"}, body: full, status: 200, wantCode: 0, wantOut: fullOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, seedAge: time.Hour, seedBody: []byte("not a book page"), seedStatus: 200, wantLookups: 1, wantStoredBody: full},
		{name: "future cache", args: []string{"show", "36451"}, body: full, status: 200, wantCode: 0, wantOut: fullOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/36451", wantClients: 1, calls: 1, seedFuture: true, seedBody: full, seedStatus: 200, wantLookups: 1},
		{name: "mixed downloads", args: []string{"show", "1"}, body: []byte(mixed), status: 200, wantCode: 0, wantOut: "title: Test\nauthor: -\npublisher: -\nyear: -\nlanguage: -\nformat: -\nsize: -\nisbn: -\npages: -\ndownloads:\n  - Fast Partner Server #1 (requires signed-in session)\n  - Slow Partner Server #1\n", wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/1", wantClients: 1, calls: 1},
		{name: "missing fields", args: []string{"show", "1"}, body: []byte(missing), status: 200, wantCode: 0, wantOut: missingOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/1", wantClients: 1, calls: 1},
		{name: "verbose", args: []string{"show", "--verbose", "1"}, body: []byte(missing), status: 200, wantCode: 0, wantOut: missingOutput, wantErr: "mirror: https://mirror.test\n", wantURL: "https://mirror.test/books/1", wantClients: 1, calls: 1, verbose: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("BOOXD_CONFIG", "")
			t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
			if tt.seedBody != nil {
				dir, err := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
				if err != nil {
					t.Fatal(err)
				}
				fetchedAt := time.Now().Add(-tt.seedAge)
				if tt.seedFuture {
					fetchedAt = time.Now().Add(time.Hour)
				}
				if err := cache.Open(dir).Put(cache.Entry{Query: "book:36451", Page: 0, Status: tt.seedStatus, FetchedAt: fetchedAt, Body: tt.seedBody}); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			clients, calls, lookups := 0, 0, 0
			var gotURL string
			newClient := func(_ config.Config, writer io.Writer, _ http.CookieJar) *fetch.Client {
				clients++
				if (writer != nil) != tt.verbose || (writer != nil && writer != &stderr) {
					t.Errorf("verbose writer = %v", writer)
				}
				return fetch.New(fetch.WithLookup(func(_ context.Context, rawURL string) (int, []byte, bool) {
					lookups++
					gotURL = rawURL
					return tt.status, tt.body, true
				}))
			}
			resolve := func(_ context.Context, _ config.Config, override string, _ *fetch.Client) (string, error) {
				calls++
				if override != "" && override != "https://override.test" {
					t.Errorf("override = %q", override)
				}
				if tt.resolveErr != nil {
					return "", tt.resolveErr
				}
				if override != "" {
					return override, nil
				}
				return "https://mirror.test", nil
			}
			code := run(tt.args, &stdout, &stderr, resolve, newClient)
			if code != tt.wantCode || stdout.String() != tt.wantOut || stderr.String() != tt.wantErr {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout.String(), stderr.String(), tt.wantCode, tt.wantOut, tt.wantErr)
			}
			if clients != tt.wantClients || calls != tt.calls || gotURL != tt.wantURL {
				t.Fatalf("clients=%d calls=%d URL=%q; want %d %d %q", clients, calls, gotURL, tt.wantClients, tt.calls, tt.wantURL)
			}
			if tt.wantLookups > 0 && lookups != tt.wantLookups {
				t.Fatalf("lookups=%d, want %d", lookups, tt.wantLookups)
			}
			if tt.name == "fresh cache" && lookups != 0 {
				t.Fatalf("fresh cache made %d lookups", lookups)
			}
			if tt.wantNoEntry || tt.wantStoredBody != nil {
				dir, err := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
				if err != nil {
					t.Fatal(err)
				}
				entry, ok := cache.Open(dir).Get("book:36451", 0)
				if tt.wantNoEntry && ok {
					t.Fatal("unexpected cache entry")
				}
				if tt.wantStoredBody != nil && (!ok || !bytes.Equal(entry.Body, tt.wantStoredBody)) {
					t.Fatal("unparseable cache entry was not replaced")
				}
			}
			if tt.name == "fresh cache" {
				dir, _ := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
				entry, ok := cache.Open(dir).Get("book:36451", 0)
				if !ok || !bytes.Equal(entry.Body, full) {
					t.Fatal("fresh cached body was not retained")
				}
			}
			if tt.name == "stale cache" {
				dir, _ := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
				entry, ok := cache.Open(dir).Get("book:36451", 0)
				if !ok || time.Since(entry.FetchedAt) >= cache.DefaultTTL {
					t.Fatalf("stale entry was not refreshed: %+v, %v", entry, ok)
				}
			}
		})
	}
}

func TestRunShowSlugSharesCacheKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BOOXD_CONFIG", "")
	t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
	dir, err := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	clients := 0
	newClient := func(_ config.Config, _ io.Writer, _ http.CookieJar) *fetch.Client {
		clients++
		return fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
			return 200, showFixture(t, "book-detail.html"), true
		}))
	}
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}
	if code := run([]string{"show", "36451-36451-test"}, &out, &stderr, resolve, newClient); code != 0 {
		t.Fatalf("slug show failed: %d %q", code, stderr.String())
	}
	out.Reset()
	stderr.Reset()
	if code := run([]string{"show", "36451"}, &out, &stderr, resolve, newClient); code != 0 || clients != 1 || stderr.Len() != 0 {
		t.Fatalf("bare id did not use slug cache: code=%d clients=%d stderr=%q", code, clients, stderr.String())
	}
	if !strings.HasPrefix(out.String(), "title: Test\n") {
		t.Fatalf("cached output: %q", out.String())
	}
	if _, ok := cache.Open(dir).Get("book:36451", 0); !ok {
		t.Fatal("cache key book:36451 missing")
	}
}
