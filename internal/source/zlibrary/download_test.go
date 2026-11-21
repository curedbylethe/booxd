package zlibrary

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/source"
)

func TestResolveAbsoluteLink(t *testing.T) {
	var method, path string
	s, _ := serve(t, true, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		_, _ = w.Write(fixture(t, "file.json"))
	})
	got, err := s.ResolveDownload(context.Background(), leGuin)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || path != "/eapi/book/5512/a1b2c3/file" {
		t.Fatalf("request = %s %s", method, path)
	}
	if got.String() != "https://cdn.example.org/dl/5512/a1b2c3?token=signed-4471&expires=99" {
		t.Fatalf("link = %s", got.String())
	}
}

func TestResolveRelativeLink(t *testing.T) {
	s, _ := serve(t, true, reply(200, fixture(t, "file-relative.json")))
	got, err := s.ResolveDownload(context.Background(), leGuin)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "https" || got.Host != s.base.Host || got.Path != "/dl/5512/a1b2c3" || got.RawQuery != "token=signed-4471" {
		t.Fatalf("link = %s", got.String())
	}
}

func TestResolveRefusesHTTPLink(t *testing.T) {
	s, _ := serve(t, true, reply(200, fixture(t, "file-http.json")))
	got, err := s.ResolveDownload(context.Background(), leGuin)
	if err == nil || got != (url.URL{}) {
		t.Fatalf("ResolveDownload = %s, %v; want refusal", got.String(), err)
	}
	if msg := err.Error(); strings.Contains(msg, "cdn.example.org") || strings.Contains(msg, "signed-4471") {
		t.Fatalf("error leaks the link: %q", msg)
	}
}

func TestResolveBadRef(t *testing.T) {
	s, calls := serve(t, true, reply(200, fixture(t, "file.json")))
	for _, ref := range []source.Ref{{Source: source.Anna, ID: "5512"}, {Source: source.ZLibrary, ID: "5512"}} {
		if _, err := s.ResolveDownload(context.Background(), ref); !errors.Is(err, ErrBadRef) {
			t.Errorf("ResolveDownload(%+v) error = %v, want ErrBadRef", ref, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("server saw %d requests, want 0", calls.Load())
	}
}

func TestQuotaAndSessionRejection(t *testing.T) {
	for _, tc := range []struct {
		name, file, msg string
		status          int
		want            error
	}{
		{"disallowed", "file-quota.json", "zlibrary: download quota exhausted: You have reached your daily limit of 10 downloads.", 200, ErrQuota},
		{"daily limit 403", "file-limit.json", "zlibrary: download quota exhausted: Daily download limit reached", 403, ErrQuota},
		{"session rejected", "error-login.json", "zlibrary: session rejected: Please login", 400, ErrSessionExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, calls := serve(t, true, reply(tc.status, fixture(t, tc.file)))
			got, err := s.ResolveDownload(context.Background(), leGuin)
			if !errors.Is(err, tc.want) || err.Error() != tc.msg {
				t.Fatalf("error = %q, want %q", err, tc.msg)
			}
			if got != (url.URL{}) || calls.Load() != 1 {
				t.Fatalf("link = %s, calls = %d; want no link and no retry", got.String(), calls.Load())
			}
		})
	}
}

func TestResolveNeverCached(t *testing.T) {
	s, calls := serve(t, false, reply(200, fixture(t, "file.json")))
	dir := t.TempDir()
	s.Store = cache.Open(dir)
	for range 2 {
		if _, err := s.ResolveDownload(context.Background(), leGuin); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 || cacheFiles(t, dir) != 0 {
		t.Fatalf("calls = %d, cache files = %d; want 2 live calls and no writes", calls.Load(), cacheFiles(t, dir))
	}
}

func TestSaveName(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"EPUB", "5512.epub"},
		{"pdf", "5512.pdf"},
		{".epub", "5512.epub"},
		{"", "5512"},
	} {
		if got := SaveName(leGuin, tc.format); got != tc.want {
			t.Errorf("SaveName(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}
