package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

const (
	zlibRef        = "zlibrary:5512/a1b2c3"
	zlibSearchJSON = `{"success":1,"books":[{"id":5512,"hash":"a1b2c3","title":"The Left Hand of Darkness","author":"Ursula K. Le Guin","year":1969,"extension":"epub","filesizeString":"1.20 MB"},{"id":"7731","hash":"ff00ee","title":"Kindred","author":"Octavia E. Butler","year":"1979","extension":"PDF","filesizeString":"3.05 MB"}]}`
	zlibDetailJSON = `{"success":1,"book":{"id":5512,"hash":"a1b2c3","title":"The Left Hand of Darkness","author":"Ursula K. Le Guin","publisher":"Ace Books","year":"1969","language":"english","extension":"epub","filesizeString":"1.20 MB","isbn":"9780441478125","pages":304,"dl":"/dl/5512/a1b2c3"}}`
	zlibShowOutput = "title: The Left Hand of Darkness\nauthor: Ursula K. Le Guin\npublisher: Ace Books\nyear: 1969\nlanguage: english\nformat: epub\nsize: 1.20 MB\nisbn: 9780441478125\npages: 304\ndownloads:\n  - zlibrary (requires signed-in session)\n"
	// Secrets the tests plant; no output may contain them.
	sessionKey = "secret-key-4471"
	linkToken  = "signed-token-9902"
)

// zlibraryServer starts a TLS EAPI double, points the zlibrary config at it,
// and returns a client seam that trusts it plus the server's request count.
func zlibraryServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, clientFunc, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	withGetEnvironment(t, t.TempDir())
	t.Setenv("BOOXD_ZLIBRARY_BASE_URL", srv.URL)
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", "")
	newClient := func(cfg config.Config, w io.Writer, jar http.CookieJar) *fetch.Client {
		// fetch has no transport option; New copies http.DefaultTransport, so swap it to trust the test certificate.
		saved := http.DefaultTransport
		http.DefaultTransport = srv.Client().Transport
		defer func() { http.DefaultTransport = saved }()
		return newFetchClient(cfg, w, jar)
	}
	return srv, newClient, calls
}

// noNetwork is a resolver and client seam that fail the test when used.
func noNetwork(t *testing.T) (resolveFunc, clientFunc) {
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		t.Fatal("mirror resolved")
		return "", nil
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
		t.Fatal("client created")
		return nil
	}
	return resolve, newClient
}

func runZLibrary(t *testing.T, args []string, newClient clientFunc) (int, string, string) {
	t.Helper()
	resolve, _ := noNetwork(t)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, resolve, newClient)
	for _, secret := range []string{sessionKey, linkToken} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatalf("output leaks %q: stdout=%q stderr=%q", secret, stdout.String(), stderr.String())
		}
	}
	return code, stdout.String(), stderr.String()
}

// writeSessionFile writes a Netscape cookie file for the test server host.
func writeSessionFile(t *testing.T, names ...string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "127.0.0.1\tFALSE\t/\tTRUE\t0\t%s\t%s\n", name, sessionKey)
	}
	path := filepath.Join(t.TempDir(), "session.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasZLibrarySession(r *http.Request) bool {
	c, err := r.Cookie("remix_userkey")
	return err == nil && c.Value == sessionKey
}

func TestSourceUnknown(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	for _, args := range [][]string{
		{"search", "--source", "bogus", "q"},
		{"show", "--source", "bogus", "36451"},
		{"get", "--source", "bogus", "36451"},
	} {
		t.Run(args[0], func(t *testing.T) {
			resolve, newClient := noNetwork(t)
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != 2 || stderr.String() != "booxd: unknown source \"bogus\"\n" {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestSourceIRCRefused(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	for _, args := range [][]string{
		{"search", "--source", "irc", "q"},
		{"show", "--source", "irc", "16d6770d2ba9"},
		{"get", "--source", "irc", "16d6770d2ba9"},
		{"show", "irc:16d6770d2ba9"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resolve, newClient := noNetwork(t)
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != 2 || stderr.String() != "booxd: irc books are not available yet\n" {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestSourcePrefixWins(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	// A zlibrary prefix beats --source anna: the zlibrary path asks for base_url.
	resolve, newClient := noNetwork(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"show", "--source", "anna", zlibRef}, &stdout, &stderr, resolve, newClient)
	if code != 2 || !strings.Contains(stderr.String(), "base_url") {
		t.Fatalf("zlibrary prefix: code=%d stderr=%q", code, stderr.String())
	}
	// An anna prefix beats --source zlibrary: the Anna path resolves a mirror.
	var gotURL string
	resolve = func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		return "https://mirror.test", nil
	}
	newClient = func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
		return fetch.New(fetch.WithLookup(func(_ context.Context, rawURL string) (int, []byte, bool) {
			gotURL = rawURL
			return 200, showFixture(t, "book-detail.html"), true
		}))
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"show", "--source", "zlibrary", "anna:36451"}, &stdout, &stderr, resolve, newClient)
	if code != 0 || gotURL != "https://mirror.test/books/36451" {
		t.Fatalf("anna prefix: code=%d url=%q stderr=%q", code, gotURL, stderr.String())
	}
}

func TestSourceZLibraryRefusesAnnaFlags(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	t.Setenv("BOOXD_ZLIBRARY_BASE_URL", "https://example.invalid")
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"search", "--source", "zlibrary", "--mirror", "https://m.test", "q"}, "mirror"},
		{[]string{"search", "--source", "zlibrary", "--dump-html", "x.html", "q"}, "dump-html"},
		{[]string{"search", "--source", "zlibrary", "--max-age", "1h", "q"}, "max-age"},
		{[]string{"show", "--mirror", "https://m.test", zlibRef}, "mirror"},
		{[]string{"get", "--mirror", "https://m.test", zlibRef}, "mirror"},
		{[]string{"get", "--cookies", "c.txt", zlibRef}, "cookies"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			resolve, newClient := noNetwork(t)
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr, resolve, newClient)
			if want := "booxd: --" + tc.flag + " applies to anna only\n"; code != 2 || stderr.String() != want {
				t.Fatalf("code=%d stderr=%q want %q", code, stderr.String(), want)
			}
		})
	}
}

func TestSourceZLibraryBareIDNeedsHash(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	resolve, newClient := noNetwork(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"show", "--source", "zlibrary", "5512"}, &stdout, &stderr, resolve, newClient)
	if code != 2 || stderr.String() != "booxd: invalid book id \"5512\"\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestSessionPrerequisites(t *testing.T) {
	sessionless := writeSessionFile(t, "sid")
	missing := filepath.Join(t.TempDir(), "missing.txt")
	for _, tc := range []struct {
		name, base, file string
		args             []string
		want             string
	}{
		{"missing base_url", "", "", []string{"search", "--source", "zlibrary", "q"}, "booxd: zlibrary needs [zlibrary] base_url or BOOXD_ZLIBRARY_BASE_URL\n"},
		{"http base_url", "http://example.invalid", "", []string{"show", zlibRef}, "booxd: [zlibrary] base_url (BOOXD_ZLIBRARY_BASE_URL) must be an https URL\n"},
		{"missing session file", "https://example.invalid", missing, []string{"show", zlibRef}, "booxd: read cookie file " + missing + ": "},
		{"session file without cookies", "https://example.invalid", sessionless, []string{"get", zlibRef}, "booxd: session file " + sessionless + " has no remix_userid or remix_userkey cookie for the [zlibrary] base_url host\n"},
		{"session for another host", "https://example.invalid", writeSessionFile(t, "remix_userid"), []string{"search", "--source", "zlibrary", "q"}, "no remix_userid or remix_userkey cookie"},
		{"get without session", "https://example.invalid", "", []string{"get", zlibRef}, "booxd: downloads need a signed-in session; set [zlibrary] session_file or BOOXD_ZLIBRARY_SESSION_FILE\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withGetEnvironment(t, t.TempDir())
			t.Setenv("BOOXD_ZLIBRARY_BASE_URL", tc.base)
			t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", tc.file)
			_, newClient := noNetwork(t)
			code, stdout, stderr := runZLibrary(t, tc.args, newClient)
			if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || strings.Count(stderr, "\n") != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q want %q", code, stdout, stderr, tc.want)
			}
		})
	}
}

func TestSessionFailures(t *testing.T) {
	for _, tc := range []struct {
		name, detail, file string
		args               []string
		want               string
	}{
		{"rejected session", `{"success":0,"error":{"message":"Invalid credentials","code":17}}`, "", []string{"show", zlibRef}, "booxd: zlibrary: session rejected: Invalid credentials; supply a fresh session file\n"},
		{"quota", zlibDetailJSON, `{"success":1,"file":{"allowDownload":false,"disallowDownloadMessage":"<p>You have reached your <b>daily limit</b> of 10 downloads.</p>"}}`, []string{"get", zlibRef}, "booxd: zlibrary: download quota exhausted: You have reached your daily limit of 10 downloads.\n"},
		{"challenge", "<!DOCTYPE html><html><body>Checking your browser</body></html>", "", []string{"search", "--source", "zlibrary", "q"}, "booxd: zlibrary: host is challenging automated requests (status 200)\n"},
		{"response error", `{"success":0,"error":"Book <b>not</b> found"}`, "", []string{"show", zlibRef}, "booxd: zlibrary: status 200: Book not found\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var downloads atomic.Int32
			_, newClient, calls := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/file"):
					_, _ = io.WriteString(w, tc.file)
				case strings.HasPrefix(r.URL.Path, "/eapi/"):
					_, _ = io.WriteString(w, tc.detail)
				default:
					downloads.Add(1)
				}
			})
			t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
			code, stdout, stderr := runZLibrary(t, tc.args, newClient)
			if code != 1 || stdout != "" || stderr != tc.want || downloads.Load() != 0 || calls.Load() == 0 {
				t.Fatalf("code=%d calls=%d downloads=%d stdout=%q stderr=%q want %q", code, calls.Load(), downloads.Load(), stdout, stderr, tc.want)
			}
		})
	}
}

func TestZLibrarySearch(t *testing.T) {
	var form, cookie atomic.Value
	_, newClient, calls := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/eapi/book/search" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		form.Store(r.PostForm.Encode())
		cookie.Store(hasZLibrarySession(r))
		_, _ = io.WriteString(w, zlibSearchJSON)
	})
	want := zlibRef + "\tThe Left Hand of Darkness\tUrsula K. Le Guin\t1969\tepub\t1.20 MB\n" +
		"zlibrary:7731/ff00ee\tKindred\tOctavia E. Butler\t1979\tPDF\t3.05 MB\n"
	args := []string{"search", "--source", "zlibrary", "--page", "2", "left", "hand"}
	for i := 1; i <= 2; i++ {
		code, stdout, stderr := runZLibrary(t, args, newClient)
		if code != 0 || stdout != want || stderr != "" {
			t.Fatalf("anonymous run %d: code=%d stdout=%q stderr=%q", i, code, stdout, stderr)
		}
	}
	if calls.Load() != 1 || form.Load() != "limit=25&message=left+hand&page=2" || cookie.Load() != false {
		t.Fatalf("anonymous: calls=%d form=%v cookie=%v; want one cached request", calls.Load(), form.Load(), cookie.Load())
	}

	// With a session the shared cache is neither read nor written.
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
	t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
	for i := 1; i <= 2; i++ {
		if code, stdout, stderr := runZLibrary(t, args, newClient); code != 0 || stdout != want {
			t.Fatalf("session run %d: code=%d stdout=%q stderr=%q", i, code, stdout, stderr)
		}
	}
	if calls.Load() != 3 || cookie.Load() != true {
		t.Fatalf("session: calls=%d cookie=%v; want two uncached requests with the session", calls.Load(), cookie.Load())
	}
}

func TestZLibrarySearchStaleFallback(t *testing.T) {
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>Checking your browser</html>")
	})
	dir, err := cache.Path(os.Getenv("BOOXD_CACHE_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	entry := cache.Entry{Key: cache.SearchKey(source.ZLibrary, "q", 1), Query: "q", Page: 1, Status: 200, FetchedAt: time.Now().Add(-48 * time.Hour), Body: []byte(zlibSearchJSON)}
	if err := cache.Open(dir).Put(entry); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runZLibrary(t, []string{"search", "--source", "zlibrary", "q"}, newClient)
	if code != 0 || !strings.HasPrefix(stdout, zlibRef+"\t") || stderr != "booxd: zlibrary: host is challenging automated requests (status 200); showing cached results\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestZLibrarySearchEmpty(t *testing.T) {
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":1,"books":[]}`)
	})
	code, stdout, stderr := runZLibrary(t, []string{"search", "--source", "zlibrary", "q"}, newClient)
	if code != 0 || stdout != "" || stderr != "no results\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestZLibraryShow(t *testing.T) {
	var path atomic.Value
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_, _ = io.WriteString(w, zlibDetailJSON)
	})
	for _, args := range [][]string{{"show", zlibRef}, {"show", "--source", "zlibrary", "5512/a1b2c3"}} {
		t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
		code, stdout, stderr := runZLibrary(t, args, newClient)
		if code != 0 || stdout != zlibShowOutput || stderr != "" || path.Load() != "/eapi/book/5512/a1b2c3" {
			t.Fatalf("%v: code=%d path=%v stdout=%q stderr=%q", args, code, path.Load(), stdout, stderr)
		}
	}
}

func TestZLibraryShowWithoutDownload(t *testing.T) {
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":1,"book":{"title":"Kindred","extension":"pdf","dl":""}}`)
	})
	code, stdout, _ := runZLibrary(t, []string{"show", zlibRef}, newClient)
	if code != 0 || !strings.HasSuffix(stdout, "pages: -\ndownloads: -\n") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestZLibraryGet(t *testing.T) {
	var eapiSession, downloadSession atomic.Bool
	var srv *httptest.Server
	srv, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/book/5512/a1b2c3":
			eapiSession.Store(hasZLibrarySession(r))
			_, _ = io.WriteString(w, zlibDetailJSON)
		case "/eapi/book/5512/a1b2c3/file":
			_, _ = fmt.Fprintf(w, `{"success":1,"file":{"allowDownload":true,"downloadLink":"%s/dl/5512?token=%s"}}`, srv.URL, linkToken)
		case "/dl/5512":
			downloadSession.Store(hasZLibrarySession(r))
			_, _ = io.WriteString(w, "book")
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
	old := openFile
	t.Cleanup(func() { openFile = old })
	opened := ""
	openFile = func(path string) error { opened = path; return nil }
	code, stdout, stderr := runZLibrary(t, []string{"get", "--open", zlibRef}, newClient)
	path := filepath.Join(os.Getenv("BOOXD_DOWNLOAD_DIR"), "5512.epub")
	if code != 0 || stdout != "saved "+path+"\n" || opened != path || !eapiSession.Load() {
		t.Fatalf("code=%d opened=%q session=%t stdout=%q stderr=%q", code, opened, eapiSession.Load(), stdout, stderr)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "book" {
		t.Fatalf("saved body=%q err=%v", got, err)
	}
	code, stdout, _ = runZLibrary(t, []string{"get", zlibRef}, newClient)
	if code != 0 || stdout != "already downloaded: "+path+"\n" {
		t.Fatalf("second get: code=%d stdout=%q", code, stdout)
	}
}

func TestZLibraryGetHidesLinkOnTransportError(t *testing.T) {
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/file") {
			// Port 1 refuses the connection, so the transfer fails in transport.
			_, _ = fmt.Fprintf(w, `{"success":1,"file":{"allowDownload":true,"downloadLink":"https://127.0.0.1:1/dl/5512?token=%s"}}`, linkToken)
			return
		}
		_, _ = io.WriteString(w, zlibDetailJSON)
	})
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userkey"))
	code, stdout, stderr := runZLibrary(t, []string{"get", zlibRef}, newClient)
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "booxd: Get 127.0.0.1:1: ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
