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

const card = `<div class="bg-white rounded-lg shadow p-4 mb-4"><h3><a href="/books/36451-test">Test</a></h3><div class="text-sm text-[#666] mt-1">Unknown author · 2022 · PDF · 51.0 MB · Books catalog</div></div>`

func TestRun(t *testing.T) {
	help := "usage: booxd search [flags] <query>\nflags must precede query arguments\n  -config string\n    \tconfig file path\n  -dump-html string\n    \twrite the fetched page to this path\n  -max-age duration\n    \tmaximum age of cached search results (default 24h0m0s)\n  -mirror string\n    \tbase URL; skip mirror discovery\n  -no-cache\n    \tskip cached search results\n  -page int\n    \tsearch result page (default 1)\n  -verbose\n    \tlog each fetch attempt to stderr\n"
	for _, tt := range []struct {
		name                          string
		args                          []string
		code                          int
		out, err, url, override, body string
		status                        int
		calls                         int
		failure, verbose              bool
	}{
		{name: "no argument", code: 2, err: "usage: booxd <search|get|show|tui|serve>\n"},
		{name: "unknown", args: []string{"nope"}, code: 2, err: "booxd: unknown command \"nope\"\n"},
		{name: "no query", args: []string{"search"}, code: 2, err: "usage: booxd search [flags] <query>\n"},
		{name: "invalid page", args: []string{"search", "--page", "0", "test"}, code: 2, err: "booxd: page must be at least 1\n"},
		{name: "hit", args: []string{"search", "test"}, code: 0, out: "36451\tTest\t\t2022\tPDF\t51.0 MB\n", err: "mirror: https://mirror.test\n", url: "https://mirror.test/search?q=test", body: card, status: 200, calls: 1},
		{name: "page", args: []string{"search", "--page", "2", "crime", "punishment"}, code: 0, err: "mirror: https://mirror.test\nno results\n", url: "https://mirror.test/search?q=crime+punishment&page=2", status: 200, calls: 1},
		{name: "override", args: []string{"search", "--mirror", "https://override.test", "test"}, code: 0, err: "mirror: https://override.test\nno results\n", url: "https://override.test/search?q=test", override: "https://override.test", status: 200, calls: 1},
		{name: "verbose", args: []string{"search", "--verbose", "test"}, code: 0, err: "mirror: https://mirror.test\nno results\n", url: "https://mirror.test/search?q=test", status: 200, calls: 1, verbose: true},
		{name: "resolver error", args: []string{"search", "test"}, code: 1, err: "booxd: no working mirror: x\n", calls: 1, failure: true},
		{name: "status", args: []string{"search", "test"}, code: 1, err: "mirror: https://mirror.test\nbooxd: search: status 429\n", url: "https://mirror.test/search?q=test", status: 429, calls: 1},
		{name: "parse error", args: []string{"search", "test"}, code: 1, err: "mirror: https://mirror.test\nbooxd: parse search: parsed 1 of 2 results\n", url: "https://mirror.test/search?q=test", body: "<div>Showing 2 results on this page</div>" + card, status: 200, calls: 1},
		{name: "unknown flag", args: []string{"search", "--nope"}, code: 2, err: "flag provided but not defined: -nope\n" + help},
		{name: "missing flag", args: []string{"search", "--mirror"}, code: 2, err: "flag needs an argument: -mirror\n" + help},
		{name: "help", args: []string{"search", "-h"}, code: 2, err: help},
		{name: "tui help", args: []string{"tui", "-h"}, code: 2, err: "usage: booxd tui [flags]\n  -mono\n    \tdisable colour\n"},
		{name: "tui unknown flag", args: []string{"tui", "--bogus"}, code: 2, err: "flag provided but not defined: -bogus\nusage: booxd tui [flags]\n  -mono\n    \tdisable colour\n"},
		{name: "get", args: []string{"get"}, code: 2, err: "usage: booxd get [flags] <id>\nflags must precede the id\n  -config string\n    \tconfig file path\n  -cookies string\n    \tNetscape cookie file; required for a download\n  -mirror string\n    \tbase URL; skip mirror discovery\n  -verbose\n    \tlog each fetch attempt to stderr\n"},
		{name: "serve", args: []string{"serve"}, code: 1, out: "booxd: serve is not implemented yet\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
			t.Setenv("BOOXD_CONFIG", "")
			var stdout, stderr bytes.Buffer
			calls, clients := 0, 0
			var gotURL string
			var created *fetch.Client
			newClient := func(_ config.Config, w io.Writer, _ http.CookieJar) *fetch.Client {
				clients++
				if (w != nil) != tt.verbose || (w != nil && w != &stderr) {
					t.Errorf("verbose writer = %v", w)
				}
				created = fetch.New(fetch.WithLookup(func(_ context.Context, rawURL string) (int, []byte, bool) {
					gotURL = rawURL
					return tt.status, []byte(tt.body), true
				}))
				return created
			}
			resolve := func(_ context.Context, _ config.Config, override string, client *fetch.Client) (string, error) {
				calls++
				if client != created {
					t.Error("resolver did not receive shared client")
				}
				if override != tt.override {
					t.Errorf("override = %q", override)
				}
				if tt.failure {
					return "", errors.New("no working mirror: x")
				}
				if override != "" {
					return override, nil
				}
				return "https://mirror.test", nil
			}
			code := run(tt.args, &stdout, &stderr, resolve, newClient)
			if code != tt.code || stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("code = %d, stdout = %q, stderr = %q; want %d, %q, %q", code, stdout.String(), stderr.String(), tt.code, tt.out, tt.err)
			}
			if calls != tt.calls || clients != tt.calls || gotURL != tt.url {
				t.Fatalf("resolver calls = %d, clients = %d, URL = %q; want %d, %d, %q", calls, clients, gotURL, tt.calls, tt.calls, tt.url)
			}
		})
	}
}

func TestMonoEnabled(t *testing.T) {
	for _, tt := range []struct {
		flagMono bool
		noColor  string
		want     bool
	}{
		{flagMono: false, noColor: "", want: false},
		{flagMono: false, noColor: "1", want: true},
		{flagMono: false, noColor: "0", want: true},
		{flagMono: true, noColor: "", want: true},
		{flagMono: true, noColor: "1", want: true},
	} {
		if got := monoEnabled(tt.flagMono, tt.noColor); got != tt.want {
			t.Errorf("monoEnabled(%t, %q) = %t, want %t", tt.flagMono, tt.noColor, got, tt.want)
		}
	}
}

func TestSearchCache(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		fresh      bool
		resolveErr bool
		status     int
		body       string
		seed       bool
		wantCached bool
		wantFetch  bool
		fetchError bool
	}{
		{name: "fresh hit", fresh: true, seed: true, wantCached: true},
		{name: "no cache", fresh: true, seed: true, args: []string{"--no-cache"}, wantFetch: true},
		{name: "max age", fresh: true, seed: true, args: []string{"--max-age", "1s"}, wantFetch: true},
		{name: "resolver fallback", resolveErr: true, seed: true, args: []string{"--max-age", "1s"}, wantCached: true},
		{name: "transport fallback", fetchError: true, seed: true, args: []string{"--max-age", "1s", "--mirror", "ftp://mirror.test"}, wantCached: true, wantFetch: true},
		{name: "429 fallback", status: 429, seed: true, args: []string{"--max-age", "1s"}, wantCached: true, wantFetch: true},
		{name: "429 miss", status: 429},
		{name: "corrupt entry refetch", fetchError: false},
		{name: "sweep on startup"},
		{name: "empty response preserves cached result", seed: true, args: []string{"--max-age", "1s"}, body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			configPath := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(configPath, []byte("cache_dir = \""+cacheDir+"\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			store := cache.Open(filepath.Join(cacheDir, "booxd", "cache"))
			if tc.name == "corrupt entry refetch" {
				if err := os.MkdirAll(filepath.Join(cacheDir, "booxd", "cache"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cacheDir, "booxd", "cache", cache.Key("test", 1)+".json"), []byte("broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "sweep on startup" {
				if err := store.Put(cache.Entry{Query: "old", Page: 1, FetchedAt: time.Now().Add(-31 * 24 * time.Hour), Body: []byte(card)}); err != nil {
					t.Fatal(err)
				}
				oldPath := filepath.Join(cacheDir, "booxd", "cache", cache.Key("old", 1)+".json")
				old := time.Now().Add(-31 * 24 * time.Hour)
				if err := os.Chtimes(oldPath, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if tc.seed {
				if err := store.Put(cache.Entry{Query: "test", Page: 1, Status: 200, FetchedAt: time.Now().Add(-3 * time.Hour), Body: []byte(card)}); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"search", "--config", configPath}, tc.args...)
			args = append(args, "test")
			var out, stderr bytes.Buffer
			clients, resolves := 0, 0
			client := func(_ config.Config, _ io.Writer, _ http.CookieJar) *fetch.Client {
				clients++
				return fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) {
					body := tc.body
					if body == "" && tc.name != "empty response preserves cached result" {
						body = card
					}
					return statusOr200(tc.status), []byte(body), !tc.fetchError
				}))
			}
			resolve := func(_ context.Context, _ config.Config, override string, _ *fetch.Client) (string, error) {
				resolves++
				if tc.resolveErr {
					return "", errors.New("offline")
				}
				if override != "" {
					return override, nil
				}
				return "https://mirror.test", nil
			}
			code := run(args, &out, &stderr, resolve, client)
			if tc.wantCached {
				if code != 0 || !strings.Contains(stderr.String(), "cached 3h ago\n") || out.String() != "36451\tTest\t\t2022\tPDF\t51.0 MB\n" {
					t.Fatalf("cached result: code=%d out=%q err=%q", code, out.String(), stderr.String())
				}
			} else if tc.status == 429 && code != 1 {
				t.Fatalf("429 miss code=%d stderr=%q", code, stderr.String())
			}
			if tc.wantFetch && (clients != 1 || resolves != 1) {
				t.Fatalf("fetch calls clients=%d resolves=%d", clients, resolves)
			}
			if tc.fresh && len(tc.args) == 0 && (clients != 0 || resolves != 0) {
				t.Fatalf("fresh hit made requests: %d/%d", clients, resolves)
			}
			if tc.name == "no cache" {
				if entry, ok := store.Get("test", 1); !ok || entry.Status != 200 {
					t.Fatal("successful --no-cache fetch was not stored")
				}
			}
			if tc.name == "corrupt entry refetch" {
				if _, ok := store.Get("test", 1); !ok {
					t.Fatal("corrupt entry was not replaced")
				}
			}
			if tc.name == "empty response preserves cached result" {
				entry, ok := store.Get("test", 1)
				if !ok || string(entry.Body) != card {
					t.Fatal("empty response replaced cached result")
				}
			}
			if tc.name == "sweep on startup" {
				if _, err := os.Stat(filepath.Join(cacheDir, "booxd", "cache", cache.Key("old", 1)+".json")); !os.IsNotExist(err) {
					t.Fatalf("old entry remains: %v", err)
				}
			}
		})
	}
}

func statusOr200(status int) int {
	if status == 0 {
		return 200
	}
	return status
}

func TestDumpHTML(t *testing.T) {
	for _, tc := range []struct {
		name               string
		body               string
		status             int
		badPath, wantError bool
	}{
		{name: "success", body: card, status: 200},
		{name: "parse failure", body: "<div>Showing 2 results on this page</div>" + card, status: 200, wantError: true},
		{name: "status failure", body: "unavailable", status: 429, wantError: true},
		{name: "write failure", body: card, status: 200, badPath: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
			path := filepath.Join(t.TempDir(), "page.html")
			if tc.badPath {
				path = filepath.Join(path, "missing", "page.html")
			}
			var out, stderr bytes.Buffer
			resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
				return "https://mirror.test", nil
			}
			client := func(_ config.Config, _ io.Writer, _ http.CookieJar) *fetch.Client {
				return fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) { return tc.status, []byte(tc.body), true }))
			}
			code := run([]string{"search", "--dump-html", path, "test"}, &out, &stderr, resolve, client)
			if (code != 0) != tc.wantError {
				t.Fatalf("code = %d, stderr = %q", code, stderr.String())
			}
			if tc.badPath {
				if !strings.Contains(stderr.String(), "booxd: ") {
					t.Fatalf("stderr = %q", stderr.String())
				}
				return
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != tc.body {
				t.Fatalf("dump = %q, %v", got, err)
			}
			if !tc.wantError && out.String() != "36451\tTest\t\t2022\tPDF\t51.0 MB\n" {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestResolveMirrorOverride(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := resolveMirror(context.Background(), config.Config{}, "https://mirror.test/", fetch.New())
	if err != nil || got != "https://mirror.test" {
		t.Fatalf("resolveMirror override = %q, %v", got, err)
	}
	if _, err := resolveMirror(context.Background(), config.Config{}, "", fetch.New()); err == nil {
		t.Fatal("discovery succeeded without a user cache directory")
	}
}

func TestRunConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
	t.Setenv("BOOXD_CONFIG", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("user_agent = \"file/1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOXD_USER_AGENT", "env/2")
	var out, stderr bytes.Buffer
	clients, resolutions := 0, 0
	wantAgent, wantOverride := "env/2", ""
	newClient := func(cfg config.Config, _ io.Writer, _ http.CookieJar) *fetch.Client {
		clients++
		if cfg.UserAgent != wantAgent {
			t.Errorf("user agent = %q, want %q", cfg.UserAgent, wantAgent)
		}
		return fetch.New(fetch.WithLookup(func(context.Context, string) (int, []byte, bool) { return 200, []byte(card), true }))
	}
	resolve := func(_ context.Context, cfg config.Config, override string, _ *fetch.Client) (string, error) {
		resolutions++
		if cfg.UserAgent != wantAgent || len(cfg.Mirrors) == 0 || override != wantOverride {
			t.Errorf("config = %#v, override = %q", cfg, override)
		}
		return "https://mirror.test", nil
	}
	code := run([]string{"search", "--config", path, "test"}, &out, &stderr, resolve, newClient)
	if code != 0 || clients != 1 || resolutions != 1 || out.String() == "" {
		t.Fatalf("code=%d clients=%d resolutions=%d stdout=%q stderr=%q", code, clients, resolutions, out.String(), stderr.String())
	}
	badPath := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(badPath, []byte("request_timeout = \"bad\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clients, resolutions = 0, 0
	out.Reset()
	stderr.Reset()
	code = run([]string{"search", "--config", badPath, "test"}, &out, &stderr, resolve, newClient)
	if code != 1 || clients != 0 || resolutions != 0 || out.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), badPath) || !strings.Contains(stderr.String(), "request_timeout") {
		t.Fatalf("malformed config: code=%d clients=%d resolutions=%d stdout=%q stderr=%q", code, clients, resolutions, out.String(), stderr.String())
	}

	otherPath := filepath.Join(t.TempDir(), "environment.toml")
	if err := os.WriteFile(otherPath, []byte("user_agent = \"environment/1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOXD_CONFIG", otherPath)
	t.Setenv("BOOXD_USER_AGENT", "")
	wantAgent = "file/1"
	out.Reset()
	stderr.Reset()
	code = run([]string{"search", "--config", path, "test"}, &out, &stderr, resolve, newClient)
	if code != 0 {
		t.Fatalf("flag config path lost to BOOXD_CONFIG: code=%d stderr=%q", code, stderr.String())
	}

	defaultHome := t.TempDir()
	t.Setenv("HOME", defaultHome)
	defaultPath, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath, []byte("user_agent = \"default-path/1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOXD_CONFIG", "")
	wantAgent = "default-path/1"
	out.Reset()
	stderr.Reset()
	code = run([]string{"search", "test"}, &out, &stderr, resolve, newClient)
	if code != 0 {
		t.Fatalf("default config path failed: code=%d stderr=%q", code, stderr.String())
	}

	out.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "missing.toml")
	code = run([]string{"search", "--config", missing, "test"}, &out, &stderr, resolve, newClient)
	if code != 1 || !strings.Contains(stderr.String(), missing) || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("explicit missing config: code=%d stderr=%q", code, stderr.String())
	}

	wantOverride = "https://override.test"
	wantAgent = "default-path/1"
	out.Reset()
	stderr.Reset()
	code = run([]string{"search", "--mirror", wantOverride, "test"}, &out, &stderr, resolve, newClient)
	if code != 0 {
		t.Fatalf("mirror flag with config failed: code=%d stderr=%q", code, stderr.String())
	}
}
