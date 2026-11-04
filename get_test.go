package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestGetRequiresOneID(t *testing.T) {
	for _, args := range [][]string{{"get"}, {"get", "42", "43"}} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
				t.Fatal("resolve called for invalid arguments")
				return "", nil
			}
			newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
				t.Fatal("client created for invalid arguments")
				return nil
			}
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != 2 || !strings.Contains(stderr.String(), "usage: booxd get [flags] <id>") {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestGetInvalidID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		t.Fatal("resolve called for invalid id")
		return "", nil
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
		t.Fatal("client created for invalid id")
		return nil
	}
	code := run([]string{"get", "bad id"}, &stdout, &stderr, resolve, newClient)
	if code != 2 || stderr.String() != "booxd: invalid book id \"bad id\"\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestGetWithoutCookies(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resolve := func(_ context.Context, _ config.Config, override string, _ *fetch.Client) (string, error) {
		return override, nil
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client { return fetch.New() }
	code := run([]string{"get", "--mirror", "https://mirror.test", "42"}, &stdout, &stderr, resolve, newClient)
	want := "mirror: https://mirror.test\nbooxd: downloads need a signed-in session; pass --cookies cookies.txt: https://mirror.test/books/42\n"
	if code != 2 || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestGetCookieFileMissing(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		t.Fatal("resolve called after cookie-file failure")
		return "", nil
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client { return fetch.New() }
	missing := filepath.Join(t.TempDir(), "missing.cookies")
	code := run([]string{"get", "--cookies", missing, "42"}, &stdout, &stderr, resolve, newClient)
	if code != 1 || !strings.Contains(stderr.String(), missing) || strings.Contains(stderr.String(), "\n\n") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestGetSendsCookieToPageAndDownload(t *testing.T) {
	var pageCookie, downloadCookie atomic.Bool
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/books/42":
			pageCookie.Store(hasSession(r))
			_, _ = io.WriteString(w, openBookPage(`<a href="/download/file">Download</a>`))
		case "/download/file":
			downloadCookie.Store(hasSession(r))
			w.Header().Set("Content-Disposition", `attachment; filename="book.epub"`)
			_, _ = io.WriteString(w, "book")
		default:
			http.NotFound(w, r)
		}
	})
	cookiePath := writeCookieFile(t, srv.URL)
	withGetEnvironment(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
	path := filepath.Join(os.Getenv("BOOXD_DOWNLOAD_DIR"), "book.epub")
	if code != 0 || stdout.String() != fmt.Sprintf("saved %s\n", path) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !pageCookie.Load() || !downloadCookie.Load() {
		t.Fatalf("session cookie page=%t download=%t", pageCookie.Load(), downloadCookie.Load())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "book" {
		t.Fatalf("saved body=%q err=%v", got, err)
	}
	if !strings.Contains(stderr.String(), "option: Fast Partner\n") || !strings.HasSuffix(stderr.String(), "100%\n") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestGetLoginWall(t *testing.T) {
	var downloads atomic.Int32
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/books/42" {
			_, _ = io.WriteString(w, loginBookPage())
			return
		}
		downloads.Add(1)
	})
	cookiePath := writeCookieFile(t, srv.URL)
	withGetEnvironment(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
	if code != 1 || downloads.Load() != 0 || !strings.Contains(stderr.String(), "booxd: downloads need a signed-in session: "+srv.URL+"/books/42\n") {
		t.Fatalf("code=%d downloads=%d stderr=%q", code, downloads.Load(), stderr.String())
	}
}

func TestGetPageStatus(t *testing.T) {
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	cookiePath := writeCookieFile(t, srv.URL)
	withGetEnvironment(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || strings.Count(strings.TrimPrefix(stderr.String(), "mirror: "+srv.URL+"\n"), "\n") != 1 || !strings.Contains(stderr.String(), "booxd: get: status 403\n") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestGetForbiddenStopsAtFirstOption(t *testing.T) {
	var first, second atomic.Int32
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/books/42":
			_, _ = io.WriteString(w, openBookPage(`<a href="/first">Download</a><a href="/second">Download</a>`))
		case "/first":
			first.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
		case "/second":
			second.Add(1)
		}
	})
	cookiePath := writeCookieFile(t, srv.URL)
	withGetEnvironment(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
	if code != 1 || first.Load() != 1 || second.Load() != 0 || !strings.Contains(stderr.String(), "booxd: download refused: Forbidden\n") {
		t.Fatalf("code=%d requests=%d/%d stderr=%q", code, first.Load(), second.Load(), stderr.String())
	}
}

func TestGetExistingFile(t *testing.T) {
	var requests atomic.Int32
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/books/42" {
			_, _ = io.WriteString(w, openBookPage(`<a href="/download/file">Download</a>`))
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Disposition", `attachment; filename="book.epub"`)
		_, _ = io.WriteString(w, "new")
	})
	cookiePath := writeCookieFile(t, srv.URL)
	dir := t.TempDir()
	withGetEnvironment(t, dir)
	path := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
	if code != 0 || stdout.String() != fmt.Sprintf("already downloaded: %s\n", path) || requests.Load() != 1 {
		t.Fatalf("code=%d stdout=%q requests=%d stderr=%q", code, stdout.String(), requests.Load(), stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "old" {
		t.Fatalf("existing file=%q err=%v", got, err)
	}
}

func withGetEnvironment(t *testing.T, downloadDir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BOOXD_CONFIG", "")
	t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
	t.Setenv("BOOXD_DOWNLOAD_DIR", downloadDir)
	t.Setenv("BOOXD_PER_HOST_INTERVAL", "1ms")
}

func runGetOnServer(t *testing.T, srv *httptest.Server, args []string, stdout, stderr io.Writer) int {
	t.Helper()
	args = append([]string{"get", "--mirror", srv.URL}, args[1:]...)
	resolve := func(_ context.Context, _ config.Config, override string, _ *fetch.Client) (string, error) {
		if override != srv.URL {
			return "", fmt.Errorf("unexpected mirror override %q", override)
		}
		return override, nil
	}
	return run(args, stdout, stderr, resolve, newFetchClient)
}

func newGetServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func openBookPage(downloadLink string) string {
	return `<div id="book-panel-details"><table><tr><td>Format</td><td>EPUB</td></tr></table></div>` +
		`<div id="book-panel-downloads"><li class="list-disc"><span class="font-medium text-[#444]">Fast Partner</span>` + downloadLink + `</li></div>`
}

func loginBookPage() string {
	return `<div id="book-panel-details"><table><tr><td>Format</td><td>EPUB</td></tr></table></div>` +
		`<div id="book-panel-downloads"><li class="list-disc"><span class="font-medium text-[#444]">Fast Partner</span><a href="https://annas-archive.is/account">Log in to access downloads</a></li></div>`
}

func writeCookieFile(t *testing.T, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cookies.txt")
	data := fmt.Sprintf("%s\tFALSE\t/\tFALSE\t0\tsid\tdummy\n", u.Hostname())
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasSession(r *http.Request) bool {
	cookie, err := r.Cookie("sid")
	return err == nil && cookie.Value == "dummy"
}
