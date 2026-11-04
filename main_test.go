package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/fetch"
)

const card = `<div class="bg-white rounded-lg shadow p-4 mb-4"><h3><a href="/books/36451-test">Test</a></h3><div class="text-sm text-[#666] mt-1">Unknown author · 2022 · PDF · 51.0 MB · Books catalog</div></div>`

func TestRun(t *testing.T) {
	help := "usage: booxd search [flags] <query>\nflags must precede query arguments\n  -dump-html string\n    \twrite the fetched page to this path\n  -mirror string\n    \tbase URL; skip mirror discovery\n  -page int\n    \tsearch result page (default 1)\n  -verbose\n    \tlog each fetch attempt to stderr\n"
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
		{name: "get", args: []string{"get"}, code: 1, out: "booxd: get is not implemented yet\n"},
		{name: "show", args: []string{"show"}, code: 1, out: "booxd: show is not implemented yet\n"},
		{name: "tui", args: []string{"tui"}, code: 1, out: "booxd: tui is not implemented yet\n"},
		{name: "serve", args: []string{"serve"}, code: 1, out: "booxd: serve is not implemented yet\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			calls, clients := 0, 0
			var gotURL string
			var created *fetch.Client
			newClient := func(w io.Writer) *fetch.Client {
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
			resolve := func(_ context.Context, override string, client *fetch.Client) (string, error) {
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
			path := filepath.Join(t.TempDir(), "page.html")
			if tc.badPath {
				path = filepath.Join(path, "missing", "page.html")
			}
			var out, stderr bytes.Buffer
			resolve := func(context.Context, string, *fetch.Client) (string, error) { return "https://mirror.test", nil }
			client := func(io.Writer) *fetch.Client {
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
	got, err := resolveMirror(context.Background(), "https://mirror.test/", fetch.New())
	if err != nil || got != "https://mirror.test" {
		t.Fatalf("resolveMirror override = %q, %v", got, err)
	}
	if _, err := resolveMirror(context.Background(), "", fetch.New()); err == nil {
		t.Fatal("discovery succeeded without a user cache directory")
	}
}
