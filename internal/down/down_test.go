package down

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/curedbylethe/booxd/internal/fetch"
)

func downClient() *fetch.Client {
	return fetch.New(fetch.WithInterval(0), fetch.WithTimeout(0))
}

func TestDownloadWritesFinalFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(w, "hello")
	}))
	defer server.Close()
	dir := t.TempDir()
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "book.epub") {
		t.Fatalf("path = %q", got)
	}
	assertFile(t, got, "hello")
	assertMissing(t, got+".part")
}

func TestResumeSendsRangeAndAppends(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", "0")
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("Range = %q", got)
		}
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "world")
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "book.epub.part"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, got, "helloworld")
	assertMissing(t, got+".part")
}

func TestServerIgnoringRangeRestarts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(w, "fresh")
	}))
	defer server.Close()
	dir := t.TempDir()
	part := filepath.Join(dir, "book.epub.part")
	if err := os.WriteFile(part, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, got, "fresh")
}

func TestRangeNotSatisfiableCompletionAndRetry(t *testing.T) {
	t.Run("complete partial", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get("Range") == "" {
				w.Header().Set("Content-Length", "0")
				return
			}
			w.Header().Set("Content-Range", "bytes */5")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		}))
		defer server.Close()
		dir := t.TempDir()
		part := filepath.Join(dir, "book.epub.part")
		if err := os.WriteFile(part, []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		var progress strings.Builder
		got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir, Progress: &progress})
		if err != nil {
			t.Fatal(err)
		}
		assertFile(t, got, "hello")
		if calls.Load() != 2 || progress.String() != "100%\n" {
			t.Fatalf("calls=%d progress=%q", calls.Load(), progress.String())
		}
	})

	t.Run("retry full response", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			if call == 1 {
				w.Header().Set("Content-Length", "0")
				return
			}
			if call == 2 {
				if r.Header.Get("Range") != "bytes=2-" {
					t.Errorf("first Range = %q", r.Header.Get("Range"))
				}
				w.Header().Set("Content-Range", "bytes */5")
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if r.Header.Get("Range") != "" {
				t.Errorf("retry Range = %q", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Length", "5")
			_, _ = io.WriteString(w, "fresh")
		}))
		defer server.Close()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "book.epub.part"), []byte("xx"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		assertFile(t, got, "fresh")
		if calls.Load() != 3 {
			t.Fatalf("calls = %d, want 3", calls.Load())
		}
	})
}

func TestTruncatedKeepsPart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, strings.Repeat("x", 40))
	}))
	defer server.Close()
	dir := t.TempDir()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "truncated download") {
		t.Fatalf("error = %v", err)
	}
	assertFile(t, filepath.Join(dir, "book.epub.part"), strings.Repeat("x", 40))
	assertMissing(t, filepath.Join(dir, "book.epub"))
}

func TestCopyFailureKeepsPart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "partial")
	}))
	defer server.Close()
	dir := t.TempDir()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err == nil {
		t.Fatal("expected copy failure")
	}
	assertFile(t, filepath.Join(dir, "book.epub.part"), "partial")
	assertMissing(t, filepath.Join(dir, "book.epub"))
}

func TestForbiddenStopsAfterOneResponse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: t.TempDir()})
	if err == nil || err.Error() != "download refused: Forbidden" || calls.Load() != 1 || strings.Contains(err.Error(), "\n") {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestProgressWholePercent(t *testing.T) {
	body := strings.Repeat("x", 100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		flusher := w.(http.Flusher)
		for i := 0; i < len(body); i += 10 {
			_, _ = io.WriteString(w, body[i:i+10])
			flusher.Flush()
		}
	}))
	defer server.Close()
	var output strings.Builder
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: t.TempDir(), Progress: &output})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) == 0 || lines[len(lines)-1] != "100%" {
		t.Fatalf("progress = %q", output.String())
	}
	last := -1
	for _, line := range lines {
		if strings.ContainsAny(line, "\r\033") || !strings.HasSuffix(line, "%") {
			t.Fatalf("invalid progress line %q", line)
		}
		value, err := strconv.Atoi(strings.TrimSuffix(line, "%"))
		if err != nil || value <= last || value > 100 {
			t.Fatalf("progress not monotone: %q after %d", line, last)
		}
		last = value
	}
}

func TestNoLengthSkipsProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "chunked")
	}))
	defer server.Close()
	var output strings.Builder
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: t.TempDir(), Progress: &output})
	if err != nil || output.Len() != 0 {
		t.Fatalf("error = %v, progress = %q", err, output.String())
	}
	assertFile(t, got, "chunked")
}

func TestContentDispositionName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="book.epub"`)
		w.Header().Set("Content-Length", "4")
		_, _ = io.WriteString(w, "data")
	}))
	defer server.Close()
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/fallback", Dir: t.TempDir(), Name: "other"})
	if err != nil || filepath.Base(got) != "book.epub" {
		t.Fatalf("path = %q, error = %v", got, err)
	}
}

func TestNameCannotEscapeDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../evil"`)
		_, _ = io.WriteString(w, "x")
	}))
	defer server.Close()
	dir := t.TempDir()
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/fallback", Dir: dir})
	if err != nil || got != filepath.Join(dir, "evil") {
		t.Fatalf("path = %q, error = %v", got, err)
	}
}

func TestRedirectFollowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/book.epub", http.StatusFound)
			return
		}
		w.Header().Set("Content-Length", "4")
		_, _ = io.WriteString(w, "data")
	}))
	defer server.Close()
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/start", Dir: t.TempDir()})
	if err != nil || filepath.Base(got) != "book.epub" {
		t.Fatalf("path = %q, error = %v", got, err)
	}
}

func TestRedirectBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		next := 1
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/"), "%d", &next)
		http.Redirect(w, r, fmt.Sprintf("/%d", next+1), http.StatusFound)
	}))
	defer server.Close()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/0", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "3 redirects") || calls.Load() != 4 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestExistingFileUntouched(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Disposition", `attachment; filename="existing.epub"`)
		w.Header().Set("Content-Length", "0")
	}))
	defer server.Close()
	dir := t.TempDir()
	final := filepath.Join(dir, "existing.epub")
	if err := os.WriteFile(final, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(final)
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir, Name: "book.epub"})
	after, _ := os.Stat(final)
	if got != final || !errors.Is(err, ErrExists) || calls.Load() != 1 || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("path = %q, error = %v, calls = %d", got, err, calls.Load())
	}
	assertFile(t, final, "keep")
}

func TestDownloadSendsJarCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "dummy" {
			t.Errorf("sid cookie = %v, error = %v", cookie, err)
		}
		_, _ = io.WriteString(w, "x")
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "dummy"}})
	client := fetch.New(fetch.WithInterval(0), fetch.WithTimeout(0), fetch.WithJar(jar))
	if _, err := Download(context.Background(), client, Request{URL: server.URL + "/book.epub", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s = %q, error = %v; want %q", path, got, err, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat %s error = %v, want not exist", path, err)
	}
}
