package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	serverURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		cookieDomain string
		wantError    string
	}{
		{name: "matching cookie domain", cookieDomain: serverURL.Hostname(), wantError: "booxd: downloads need a signed-in session: " + srv.URL + "/books/42\n"},
		{name: "no cookie for mirror host", cookieDomain: "other.example", wantError: fmt.Sprintf("booxd: downloads need a signed-in session; cookie file has no cookies for mirror host %s. Use --mirror URL to choose a mirror matching the cookie domain: %s/books/42\n", serverURL.Host, srv.URL)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookiePath := writeCookieFileForDomain(t, tc.cookieDomain)
			withGetEnvironment(t, t.TempDir())
			var stdout, stderr bytes.Buffer
			code := runGetOnServer(t, srv, []string{"get", "--cookies", cookiePath, "42"}, &stdout, &stderr)
			if code != 1 || downloads.Load() != 0 || !strings.Contains(stderr.String(), tc.wantError) {
				t.Fatalf("code=%d downloads=%d stderr=%q", code, downloads.Load(), stderr.String())
			}
		})
	}
}

func TestGetUsesSharedClientAndPageDeadline(t *testing.T) {
	srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, loginBookPage())
	})
	cookiePath := writeCookieFile(t, srv.URL)
	withGetEnvironment(t, t.TempDir())
	t.Setenv("BOOXD_REQUEST_TIMEOUT", "10ms")
	var clients int
	var client *fetch.Client
	var clientTimeout time.Duration
	resolve := func(_ context.Context, _ config.Config, override string, got *fetch.Client) (string, error) {
		if got != client {
			t.Fatal("resolve received a different client")
		}
		return override, nil
	}
	newClient := func(cfg config.Config, verbose io.Writer, jar http.CookieJar) *fetch.Client {
		clients++
		clientTimeout = cfg.RequestTimeout
		client = newFetchClient(cfg, verbose, jar)
		return client
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "--mirror", srv.URL, "--cookies", cookiePath, "42"}, &stdout, &stderr, resolve, newClient)
	if code != 1 || clients != 1 || clientTimeout != 0 || !strings.Contains(stderr.String(), "context deadline exceeded") {
		t.Fatalf("code=%d clients=%d clientTimeout=%s stderr=%q", code, clients, clientTimeout, stderr.String())
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
	return writeCookieFileForDomain(t, u.Hostname())
}

func writeCookieFileForDomain(t *testing.T, domain string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cookies.txt")
	data := fmt.Sprintf("%s\tFALSE\t/\tFALSE\t0\tsid\tdummy\n", domain)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasSession(r *http.Request) bool {
	cookie, err := r.Cookie("sid")
	return err == nil && cookie.Value == "dummy"
}

func TestGetOpen(t *testing.T) {
	for _, tc := range []struct {
		name              string
		requested, exists bool
		failure           error
	}{
		{"calls termux-open", true, false, nil},
		{"missing command", true, false, &exec.Error{Name: "termux-open", Err: exec.ErrNotFound}},
		{"not requested", false, false, nil},
		{"already downloaded", true, true, nil},
		{"run failure", true, false, errors.New("exit status 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			withGetEnvironment(t, dir)
			path := filepath.Join(dir, "book.epub")
			if tc.exists {
				if err := os.WriteFile(path, []byte("book"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			srv := newGetServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/books/42" {
					_, _ = io.WriteString(w, openBookPage(`<a href="/download/file">Download</a>`))
					return
				}
				w.Header().Set("Content-Disposition", `attachment; filename="book.epub"`)
				_, _ = io.WriteString(w, "book")
			})
			old := openFile
			t.Cleanup(func() { openFile = old })
			calls := 0
			openFile = func(got string) error {
				calls++
				if got != path {
					t.Fatalf("path=%q want=%q", got, path)
				}
				if body, err := os.ReadFile(got); err != nil || string(body) != "book" {
					t.Fatalf("finished file=%q err=%v", body, err)
				}
				return tc.failure
			}
			args := []string{"get", "--cookies", writeCookieFile(t, srv.URL)}
			if tc.requested {
				args = append(args, "--open")
			}
			args = append(args, "42")
			var stdout, stderr bytes.Buffer
			code := runGetOnServer(t, srv, args, &stdout, &stderr)
			wantCode, wantCalls := 0, 0
			if tc.requested {
				wantCalls = 1
			}
			wantOut := fmt.Sprintf("saved %s\n", path)
			if tc.exists {
				wantOut = fmt.Sprintf("already downloaded: %s\n", path)
			}
			if tc.failure != nil {
				wantCode = 1
				var wantError string
				if runtime.GOOS == "android" && errors.Is(tc.failure, exec.ErrNotFound) {
					wantError = fmt.Sprintf("booxd: cannot open %s: termux-open not found; install termux-tools\n", path)
				} else {
					wantError = fmt.Sprintf("booxd: saved file %s but cannot open it: %s\n", path, tc.failure)
				}
				if !strings.HasSuffix(stderr.String(), wantError) {
					t.Fatalf("stderr=%q want suffix=%q", stderr.String(), wantError)
				}
			}
			if code != wantCode || calls != wantCalls || stdout.String() != wantOut {
				t.Fatalf("code=%d calls=%d stdout=%q stderr=%q", code, calls, stdout.String(), stderr.String())
			}
		})
	}
}
