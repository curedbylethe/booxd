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
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/curedbylethe/booxd/internal/tui"
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

func TestSessionJarScopedToBaseOrigin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.txt")
	// A dot-domain cookie the stdlib jar would send to every subdomain.
	line := ".example.org\tTRUE\t/\tTRUE\t0\tremix_userkey\tsessionKey\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ZLibraryBaseURL: "https://example.org", ZLibrarySessionFile: path}
	base, jar, err := zlibrarySession(cfg)
	if err != nil || jar == nil {
		t.Fatalf("zlibrarySession: err=%v jar=%v", err, jar)
	}
	for _, raw := range []string{"https://cdn.example.org", "https://example.org:8443", "http://example.org"} {
		other, perr := url.Parse(raw)
		if perr != nil {
			t.Fatal(perr)
		}
		if cookies := jar.Cookies(other); len(cookies) != 0 {
			t.Errorf("jar.Cookies(%s) leaked %d cookie(s)", raw, len(cookies))
		}
		jar.SetCookies(other, []*http.Cookie{{Name: "remix_userkey", Value: "attacker"}})
	}
	if cookies := jar.Cookies(base); len(cookies) != 1 || cookies[0].Value != "sessionKey" {
		t.Fatalf("jar.Cookies(base) = %v, want one remix_userkey=sessionKey", cookies)
	}
}

func TestSessionJarHostnameCaseInsensitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.txt")
	if err := os.WriteFile(path, []byte("example.org\tFALSE\t/\tTRUE\t0\tremix_userkey\tsessionKey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://EXAMPLE.ORG", "https://example.org"} {
		_, jar, err := zlibrarySession(config.Config{ZLibraryBaseURL: base, ZLibrarySessionFile: path})
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{"https://example.org/file", "https://EXAMPLE.org/file"} {
			u, _ := url.Parse(raw)
			if cookies := jar.Cookies(u); len(cookies) != 1 {
				t.Errorf("base %s: jar.Cookies(%s) = %v, want the session cookie", base, raw, cookies)
			}
		}
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

// A hostile response must not reach stdout with terminal control bytes.
func TestZLibrarySearchStripsControlCharacters(t *testing.T) {
	_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"success":1,"books":[{"id":5512,"hash":"a1b2c3","title":"T\u001b[2Jitle\nMore","author":"A\u001b7uthor","year":1969,"extension":"epub","filesizeString":"1.20 MB"}]}`)
	})
	code, stdout, stderr := runZLibrary(t, []string{"search", "--source", "zlibrary", "q"}, newClient)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if i := strings.IndexFunc(stdout, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' }); i >= 0 {
		t.Fatalf("stdout carries control rune %q at %d: %q", stdout[i], i, stdout)
	}
	if want := "zlibrary:5512/a1b2c3\tT[2JitleMore\tA7uthor\t1969\tepub\t1.20 MB\n"; stdout != want {
		t.Fatalf("stdout=%q want %q", stdout, want)
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
	entry := cache.Entry{Key: cache.SearchKey(source.ZLibrary, "q", source.SearchOptions{Page: 1}), Query: "q", Page: 1, Status: 200, FetchedAt: time.Now().Add(-48 * time.Hour), Body: []byte(zlibSearchJSON)}
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

// zlibraryLinkServer serves the detail and a resolved link carrying linkToken;
// dl answers the transfer.
func zlibraryLinkServer(t *testing.T, dl http.HandlerFunc) clientFunc {
	t.Helper()
	var srv *httptest.Server
	srv, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/book/5512/a1b2c3":
			_, _ = io.WriteString(w, zlibDetailJSON)
		case "/eapi/book/5512/a1b2c3/file":
			_, _ = fmt.Fprintf(w, `{"success":1,"file":{"allowDownload":true,"downloadLink":"%s/dl/5512?token=%s"}}`, srv.URL, linkToken)
		case "/dl/5512":
			dl(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
	return newClient
}

func TestZLibraryGetTransferFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		dl   http.HandlerFunc
		want string
	}{
		{"401", statusHandler(http.StatusUnauthorized), "booxd: zlibrary: download link expired or refused (status 401); run get again\n"},
		{"403", statusHandler(http.StatusForbidden), "booxd: zlibrary: download link expired or refused (status 403); run get again\n"},
		{"404", statusHandler(http.StatusNotFound), "booxd: zlibrary: download link expired or refused (status 404); run get again\n"},
		{"410", statusHandler(http.StatusGone), "booxd: zlibrary: download link expired or refused (status 410); run get again\n"},
		{"web page", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html><body>Sign in "+linkToken+"</body></html>")
		}, "booxd: down: server sent a web page or an empty body, not a book\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newClient := zlibraryLinkServer(t, tc.dl)
			code, stdout, stderr := runZLibrary(t, []string{"get", zlibRef}, newClient)
			if code != 1 || stdout != "" || stderr != tc.want {
				t.Fatalf("code=%d stdout=%q stderr=%q want %q", code, stdout, stderr, tc.want)
			}
			if entries, err := os.ReadDir(os.Getenv("BOOXD_DOWNLOAD_DIR")); err != nil || len(entries) != 0 {
				t.Fatalf("download dir entries=%v err=%v, want empty", entries, err)
			}
			// The TUI row reads "failed": the error wraps none of its outcomes.
			opts, _, _ := runTui(t, []string{"tui"}, newClient)
			_, err := opts.Download(context.Background(), source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}, func(int64, int64) {})
			for _, outcome := range []error{tui.ErrExists, tui.ErrSessionExpired, tui.ErrQuota, tui.ErrFailed, context.Canceled} {
				if err == nil || errors.Is(err, outcome) || strings.Contains(err.Error(), linkToken) {
					t.Fatalf("tui err=%v", err)
				}
			}
		})
	}
}

func statusHandler(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, linkToken, code) }
}

func TestZLibraryHostUnavailable(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	// Port 1 refuses the connection before any request reaches a host.
	t.Setenv("BOOXD_ZLIBRARY_BASE_URL", "https://127.0.0.1:1/?token="+linkToken)
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
	const want = "zlibrary: host did not answer: 127.0.0.1:1"
	for _, args := range [][]string{
		{"search", "--source", "zlibrary", "q"},
		{"show", zlibRef},
		{"get", zlibRef},
	} {
		t.Run(args[0], func(t *testing.T) {
			code, stdout, stderr := runZLibrary(t, args, newFetchClient)
			if code != 1 || stdout != "" || stderr != "booxd: "+want+"\n" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
	opts, code, _ := runTui(t, []string{"tui"}, newFetchClient)
	if code != 0 {
		t.Fatalf("tui code=%d", code)
	}
	ctx := context.Background()
	if _, err := opts.Search(ctx, source.ZLibrary, "q", 1); err == nil || err.Error() != want {
		t.Fatalf("tui search error=%v, want %q", err, want)
	}
	if _, err := opts.Detail(ctx, source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}); err == nil || err.Error() != want {
		t.Fatalf("tui detail error=%v, want %q", err, want)
	}
}

func TestTuiZLibraryDownloadCancel(t *testing.T) {
	ref := source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}
	for _, step := range []string{"resolve", "transfer"} {
		t.Run(step, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// The handler cancels the download, then holds the request open until the client hangs up.
			hold := func(w http.ResponseWriter, r *http.Request) {
				cancel()
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}
			var srv *httptest.Server
			srv, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/eapi/book/5512/a1b2c3":
					_, _ = io.WriteString(w, zlibDetailJSON)
				case "/eapi/book/5512/a1b2c3/file":
					if step == "resolve" {
						hold(w, r)
						return
					}
					_, _ = fmt.Fprintf(w, `{"success":1,"file":{"allowDownload":true,"downloadLink":"%s/dl/5512?token=%s"}}`, srv.URL, linkToken)
				case "/dl/5512":
					w.Header().Set("Content-Length", "1000")
					_, _ = io.WriteString(w, "partial book")
					w.(http.Flusher).Flush()
					hold(w, r)
				}
			})
			t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
			opts, code, _ := runTui(t, []string{"tui"}, newClient)
			if code != 0 {
				t.Fatalf("code=%d", code)
			}
			_, err := opts.Download(ctx, ref, func(int64, int64) {})
			if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), linkToken) {
				t.Fatalf("err=%v, want context.Canceled without the link", err)
			}
		})
	}
}

// captureTUI replaces the terminal interface and returns the options the tui
// command built.
func captureTUI(t *testing.T) *tui.Options {
	t.Helper()
	var got tui.Options
	old := runTUI
	t.Cleanup(func() { runTUI = old })
	runTUI = func(_ context.Context, opts tui.Options) error {
		got = opts
		return nil
	}
	return &got
}

func runTui(t *testing.T, args []string, newClient clientFunc) (*tui.Options, int, string) {
	t.Helper()
	opts := captureTUI(t)
	resolve, _ := noNetwork(t)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, resolve, newClient)
	return opts, code, stderr.String()
}

func TestTuiSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		want source.Name
		err  string
	}{
		{"default is anna", []string{"tui"}, 0, source.Anna, ""},
		{"zlibrary", []string{"tui", "--source", "zlibrary"}, 0, source.ZLibrary, ""},
		{"irc refused", []string{"tui", "--source", "irc"}, 2, "", "booxd: irc books are not available yet\n"},
		{"unknown refused", []string{"tui", "--source", "bogus"}, 2, "", "booxd: unknown source \"bogus\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withGetEnvironment(t, t.TempDir())
			t.Setenv("BOOXD_ZLIBRARY_BASE_URL", "")
			newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client { return fetch.New() }
			opts, code, stderr := runTui(t, tc.args, newClient)
			if code != tc.code || stderr != tc.err || opts.Source != tc.want {
				t.Fatalf("code=%d stderr=%q source=%q, want %d %q %q", code, stderr, opts.Source, tc.code, tc.err, tc.want)
			}
		})
	}
}

func TestTuiZLibraryWithoutBaseURL(t *testing.T) {
	withGetEnvironment(t, t.TempDir())
	t.Setenv("BOOXD_ZLIBRARY_BASE_URL", "")
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client { return fetch.New() }
	opts, code, stderr := runTui(t, []string{"tui"}, newClient)
	if code != 0 || stderr != "" || opts.Source != source.Anna {
		t.Fatalf("tui did not start on anna: code=%d stderr=%q source=%q", code, stderr, opts.Source)
	}
	const want = "zlibrary needs [zlibrary] base_url or BOOXD_ZLIBRARY_BASE_URL"
	ctx := context.Background()
	if _, err := opts.Search(ctx, source.ZLibrary, "q", 1); err == nil || err.Error() != want {
		t.Fatalf("search error=%v, want %q", err, want)
	}
	ref := source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}
	if _, err := opts.Detail(ctx, ref); err == nil || err.Error() != want {
		t.Fatalf("detail error=%v, want %q", err, want)
	}
	if _, err := opts.Download(ctx, ref, func(int64, int64) {}); !errors.Is(err, tui.ErrFailed) || err.Error() != "failed: "+want {
		t.Fatalf("download error=%v", err)
	}
}

func TestTuiZLibraryDownload(t *testing.T) {
	var srv *httptest.Server
	srv, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/book/search":
			_, _ = io.WriteString(w, zlibSearchJSON)
		case "/eapi/book/5512/a1b2c3":
			_, _ = io.WriteString(w, zlibDetailJSON)
		case "/eapi/book/5512/a1b2c3/file":
			_, _ = fmt.Fprintf(w, `{"success":1,"file":{"allowDownload":true,"downloadLink":"%s/dl/5512?token=%s"}}`, srv.URL, linkToken)
		case "/dl/5512":
			_, _ = io.WriteString(w, "book")
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
	opts, code, stderr := runTui(t, []string{"tui", "--source", "zlibrary"}, newClient)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	ctx := context.Background()
	books, err := opts.Search(ctx, source.ZLibrary, "left hand", 1)
	if err != nil || len(books) != 2 || books[0].ID.String() != zlibRef {
		t.Fatalf("search books=%v err=%v", books, err)
	}
	detail, err := opts.Detail(ctx, books[0].ID)
	if err != nil || detail.Title != "The Left Hand of Darkness" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	var steps []int64
	var total int64
	path, err := opts.Download(ctx, books[0].ID, func(done, t int64) {
		total = t
		steps = append(steps, done)
	})
	want := filepath.Join(os.Getenv("BOOXD_DOWNLOAD_DIR"), "5512.epub")
	if err != nil || path != want || len(steps) == 0 || steps[len(steps)-1] != 4 {
		t.Fatalf("download path=%q err=%v steps=%v", path, err, steps)
	}
	if total != 4 {
		t.Errorf("progress total=%d, want 4", total)
	}
	if _, err := opts.Download(ctx, books[0].ID, func(int64, int64) {}); !errors.Is(err, tui.ErrExists) {
		t.Fatalf("second download error=%v, want ErrExists", err)
	}
}

func TestTuiZLibraryDownloadOutcomes(t *testing.T) {
	ref := source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}
	for _, tc := range []struct {
		name, detail, file string
		session            bool
		want               error
	}{
		{"session expired", `{"success":0,"error":{"message":"Invalid credentials","code":17}}`, "", true, tui.ErrSessionExpired},
		{"quota", zlibDetailJSON, `{"success":1,"file":{"allowDownload":false,"disallowDownloadMessage":"daily limit"}}`, true, tui.ErrQuota},
		{"no session", zlibDetailJSON, "", false, tui.ErrFailed},
		{"transport", zlibDetailJSON, `{"success":1,"file":{"allowDownload":true,"downloadLink":"https://127.0.0.1:1/dl/5512?token=` + linkToken + `"}}`, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, newClient, calls := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/file") {
					_, _ = io.WriteString(w, tc.file)
					return
				}
				_, _ = io.WriteString(w, tc.detail)
			})
			if tc.session {
				t.Setenv("BOOXD_ZLIBRARY_SESSION_FILE", writeSessionFile(t, "remix_userid", "remix_userkey"))
			}
			opts, code, _ := runTui(t, []string{"tui"}, newClient)
			if code != 0 {
				t.Fatalf("code=%d", code)
			}
			path, err := opts.Download(context.Background(), ref, func(int64, int64) {})
			if path != "" || err == nil {
				t.Fatalf("path=%q err=%v", path, err)
			}
			for _, outcome := range []error{tui.ErrExists, tui.ErrSessionExpired, tui.ErrQuota, tui.ErrFailed} {
				if errors.Is(err, outcome) != (outcome == tc.want) {
					t.Fatalf("err=%v, want outcome %v", err, tc.want)
				}
			}
			if tc.want == tui.ErrFailed && (err.Error() != "failed: "+errNeedSession.Error() || calls.Load() != 0) {
				t.Fatalf("no-session err=%q calls=%d", err, calls.Load())
			}
		})
	}
}

func TestTuiSearchForwardsPage(t *testing.T) {
	t.Run("zlibrary", func(t *testing.T) {
		var form atomic.Value
		_, newClient, _ := zlibraryServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			form.Store(r.PostForm.Encode())
			_, _ = io.WriteString(w, zlibSearchJSON)
		})
		opts, code, stderr := runTui(t, []string{"tui", "--source", "zlibrary"}, newClient)
		if code != 0 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if _, err := opts.Search(context.Background(), source.ZLibrary, "left hand", 3); err != nil {
			t.Fatal(err)
		}
		if got, want := form.Load(), "limit=25&message=left+hand&page=3"; got != want {
			t.Fatalf("form = %v, want %q", got, want)
		}
	})
	t.Run("anna", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("BOOXD_CACHE_DIR", t.TempDir())
		t.Setenv("BOOXD_CONFIG", "")
		var gotURL string
		newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
			return fetch.New(fetch.WithLookup(func(_ context.Context, rawURL string) (int, []byte, bool) {
				gotURL = rawURL
				return 200, []byte("<div>Showing 0 results on this page</div>"), true
			}))
		}
		resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
			return "https://mirror.test", nil
		}
		opts := captureTUI(t)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"tui"}, &stdout, &stderr, resolve, newClient); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if _, err := opts.Search(context.Background(), source.Anna, "left hand", 3); err != nil {
			t.Fatal(err)
		}
		if want := "https://mirror.test/search?q=left+hand&page=3"; gotURL != want {
			t.Fatalf("url = %q, want %q", gotURL, want)
		}
	})
}
