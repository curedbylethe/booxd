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
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", "0")
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=5-" || r.Header.Get("If-Range") != `"v1"` {
			t.Errorf("Range = %q, If-Range = %q", got, r.Header.Get("If-Range"))
		}
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "world")
	}))
	defer server.Close()
	dir := t.TempDir()
	writePart(t, server.URL+"/book.epub", filepath.Join(dir, "book.epub.part"), "hello")
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, got, "helloworld")
	assertMissing(t, got+".part")
	assertMissing(t, got+".part.meta")
}

func TestResumeRejectsInvalidContentRange(t *testing.T) {
	for _, test := range []struct {
		name         string
		contentRange string
		wantError    string
	}{
		{name: "wrong start", contentRange: "bytes 4-9/10", wantError: "invalid Content-Range start"},
		{name: "wrong total", contentRange: "bytes 5-9/12", wantError: "partial cleared"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v1"`)
				if r.Header.Get("Range") == "" {
					w.Header().Set("Content-Length", "0")
					return
				}
				w.Header().Set("Content-Range", test.contentRange)
				w.Header().Set("Content-Length", "5")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "world")
			}))
			defer server.Close()
			dir := t.TempDir()
			part := filepath.Join(dir, "book.epub.part")
			writePart(t, server.URL+"/book.epub", part, "hello")
			_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
			assertFile(t, part, "")
			assertMissing(t, filepath.Join(dir, "book.epub"))
		})
	}
}

func TestServerIgnoringRangeRestarts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(w, "fresh")
	}))
	defer server.Close()
	dir := t.TempDir()
	part := filepath.Join(dir, "book.epub.part")
	writePart(t, server.URL+"/book.epub", part, "stale")
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
			w.Header().Set("ETag", `"v1"`)
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
		writePart(t, server.URL+"/book.epub", part, "hello")
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
			w.Header().Set("ETag", `"v1"`)
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
		writePart(t, server.URL+"/book.epub", filepath.Join(dir, "book.epub.part"), "xx")
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

// The byte callback must report every write, with or without a known size.
func TestBytesCallbackReportsEveryWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length bool
	}{{"known total", true}, {"unknown total", false}} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", 100)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.length {
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				}
				flusher := w.(http.Flusher)
				for i := 0; i < len(body); i += 10 {
					_, _ = io.WriteString(w, body[i:i+10])
					flusher.Flush()
				}
			}))
			defer server.Close()
			var calls, done, total int64
			req := Request{URL: server.URL + "/book.epub", Dir: t.TempDir()}
			req.Bytes = func(d, t int64) {
				calls++
				done, total = d, t
			}
			if _, err := Download(context.Background(), downClient(), req); err != nil {
				t.Fatal(err)
			}
			wantTotal := int64(-1)
			if tc.length {
				wantTotal = 100
			}
			// Without a length the transport may coalesce the flushed
			// chunks; two reports still prove unknown sizes are reported.
			if calls < 2 || done != 100 || total != wantTotal {
				t.Fatalf("calls=%d done=%d total=%d, want >=2 calls, done=100, total=%d", calls, done, total, wantTotal)
			}
		})
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

func TestNameRejectsControlCharacters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''%1b%5b2Jx.epub")
		w.Header().Set("Content-Length", "1")
		_, _ = io.WriteString(w, "x")
	}))
	defer server.Close()
	dir := t.TempDir()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/fallback.epub", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "filename is empty or invalid") {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("directory entries = %v, error = %v", entries, err)
	}
}

func TestBareURLPathUsesFallbackName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1")
		_, _ = io.WriteString(w, "x")
	}))
	defer server.Close()
	dir := t.TempDir()
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/x/0/0", Dir: dir, Name: "42.epub"})
	if err != nil || got != filepath.Join(dir, "42.epub") {
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

func TestRedirectCrossOriginDropsCookie(t *testing.T) {
	file := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("cross-origin Cookie = %q", got)
		}
		w.Header().Set("Content-Length", "4")
		_, _ = io.WriteString(w, "data")
	}))
	defer file.Close()
	// The jar scopes cookies by host, not port, so the second origin needs another host name.
	fileURL := strings.Replace(file.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "dummy" {
			t.Errorf("sid cookie = %v, error = %v", cookie, err)
		}
		http.Redirect(w, r, fileURL+"/book.epub", http.StatusFound)
	}))
	defer origin.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(origin.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "dummy"}})
	client := fetch.New(fetch.WithInterval(0), fetch.WithTimeout(0), fetch.WithJar(jar))
	got, err := Download(context.Background(), client, Request{URL: origin.URL + "/start", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, got, "data")
}

func TestRedirectSameOriginKeepsRangeAndCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "dummy" {
			t.Errorf("%s sid cookie = %v, error = %v", r.URL.Path, cookie, err)
		}
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", "0")
			return
		}
		if r.URL.Path == "/book.epub" {
			http.Redirect(w, r, "/cdn/book.epub", http.StatusFound)
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("redirected Range = %q", got)
		}
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "world")
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "dummy"}})
	client := fetch.New(fetch.WithInterval(0), fetch.WithTimeout(0), fetch.WithJar(jar))
	dir := t.TempDir()
	writePart(t, server.URL+"/book.epub", filepath.Join(dir, "book.epub.part"), "hello")
	got, err := Download(context.Background(), client, Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, got, "helloworld")
}

func TestRedirectHTTPSDowngradeRefused(t *testing.T) {
	var plainCalls atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainCalls.Add(1)
		_, _ = io.WriteString(w, "x")
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/book.epub", http.StatusFound)
	}))
	defer secure.Close()
	// fetch has no transport option; New copies http.DefaultTransport, so swap it to trust the test certificate.
	saved := http.DefaultTransport
	http.DefaultTransport = secure.Client().Transport
	defer func() { http.DefaultTransport = saved }()
	dir := t.TempDir()
	_, err := Download(context.Background(), downClient(), Request{URL: secure.URL + "/start", Dir: dir})
	if !errors.Is(err, ErrInsecureRedirect) || plainCalls.Load() != 0 {
		t.Fatalf("error = %v, plain calls = %d", err, plainCalls.Load())
	}
	assertMissing(t, filepath.Join(dir, "book.epub"))
}

func TestRedirectErrorHidesURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "ftp://cdn.example/x?token=secret")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/start", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), `unsupported scheme "ftp"`) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/x") {
		t.Fatalf("error = %v", err)
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

func TestNotBookRemovesPart(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "html content type", contentType: "text/html; charset=utf-8", body: "PK\x03\x04"},
		{name: "xhtml content type", contentType: "application/xhtml+xml", body: "PK\x03\x04"},
		{name: "sniffed html", contentType: "application/epub+zip", body: "\n<!DOCTYPE html><html><body>log in</body></html>"},
		{name: "empty body", contentType: "application/epub+zip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Content-Length", fmt.Sprint(len(test.body)))
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			dir := t.TempDir()
			part := filepath.Join(dir, "book.epub.part")
			if err := os.WriteFile(part, []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub?token=secret", Dir: dir})
			if !errors.Is(err, ErrNotBook) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "book.epub") {
				t.Fatalf("error = %v, want ErrNotBook without URL", err)
			}
			assertMissing(t, part)
			assertMissing(t, part+".meta")
			assertMissing(t, filepath.Join(dir, "book.epub"))
		})
	}
}

func TestPartialWithoutMatchingSidecarRestarts(t *testing.T) {
	for _, test := range []struct {
		name string
		meta string
	}{
		{name: "no sidecar"},
		{name: "validator changed", meta: "\"v0\"\n5\n%s\n"},
		{name: "total changed", meta: "\"v1\"\n9\n%s\n"},
		{name: "no validator stored", meta: "\n5\n%s\n"},
		{name: "other resource", meta: "\"v1\"\n5\nother.example/book.epub\n"},
		{name: "no identity stored", meta: "\"v1\"\n5\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ranged atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "" {
					ranged.Add(1)
				}
				w.Header().Set("ETag", `"v1"`)
				w.Header().Set("Content-Length", "5")
				_, _ = io.WriteString(w, "fresh")
			}))
			defer server.Close()
			dir := t.TempDir()
			part := filepath.Join(dir, "book.epub.part")
			if err := os.WriteFile(part, []byte("sta"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.meta != "" {
				su, _ := url.Parse(server.URL)
				meta := strings.ReplaceAll(test.meta, "%s", identityOf(su)+"/book.epub")
				if err := os.WriteFile(part+".meta", []byte(meta), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
			if err != nil || ranged.Load() != 0 {
				t.Fatalf("error = %v, ranged requests = %d", err, ranged.Load())
			}
			assertFile(t, got, "fresh")
			assertMissing(t, part+".meta")
		})
	}
}

func TestInterruptedDownloadResumesWithSidecar(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("ETag", `W/"weak"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		switch {
		case calls.Load() == 1:
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "hello")
		case r.Header.Get("Range") == "":
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "helloworld")
		default:
			if r.Header.Get("Range") != "bytes=5-" || r.Header.Get("If-Range") != "Mon, 02 Jan 2006 15:04:05 GMT" {
				t.Errorf("Range = %q, If-Range = %q", r.Header.Get("Range"), r.Header.Get("If-Range"))
			}
			w.Header().Set("Content-Range", "bytes 5-9/10")
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "world")
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	part := filepath.Join(dir, "book.epub.part")
	if _, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir}); err == nil {
		t.Fatal("expected truncated download")
	}
	su, _ := url.Parse(server.URL)
	assertFile(t, part+".meta", "Mon, 02 Jan 2006 15:04:05 GMT\n10\n"+identityOf(su)+"/book.epub\n")
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub", Dir: dir})
	if err != nil || calls.Load() != 3 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
	assertFile(t, got, "helloworld")
	assertMissing(t, part)
	assertMissing(t, part+".meta")
}

func TestPartialFromAnotherURLWithSameValidatorRestarts(t *testing.T) {
	var ranged atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
		}
		body := map[string]string{"/a": "AAAAAAAAAA", "/b": "BBBBBBBBBB"}[r.URL.Path]
		w.Header().Set("ETag", `"1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="book.epub"`)
		w.Header().Set("Content-Length", "10")
		if r.URL.Path == "/a" {
			// The transfer of /a is cut short after 5 bytes.
			body = body[:5]
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	dir := t.TempDir()
	if _, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/a", Dir: dir}); err == nil {
		t.Fatal("expected truncated download")
	}
	got, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/b", Dir: dir})
	if err != nil || ranged.Load() != 0 {
		t.Fatalf("error = %v, ranged requests = %d", err, ranged.Load())
	}
	assertFile(t, got, "BBBBBBBBBB")
}

func TestStatusErrorTyped(t *testing.T) {
	for _, test := range []struct {
		code int
		want string
	}{
		{code: http.StatusUnauthorized, want: "download refused: Unauthorized"},
		{code: http.StatusNotFound, want: "download failed: status 404"},
		{code: http.StatusGone, want: "download failed: status 410"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(test.code)
		}))
		_, err := Download(context.Background(), downClient(), Request{URL: server.URL + "/book.epub?token=secret", Dir: t.TempDir()})
		server.Close()
		var status *StatusError
		if !errors.As(err, &status) || status.Code != test.code || err.Error() != test.want {
			t.Fatalf("error = %v, want StatusError %q", err, test.want)
		}
	}
}

// writePart writes a partial file with a sidecar binding it to ETag "v1" and
// the resource at rawURL.
func writePart(t *testing.T, rawURL, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".meta", []byte("\"v1\"\n\n"+identityOf(u)+"\n"), 0o600); err != nil {
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
