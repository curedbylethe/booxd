package zlibrary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

const challengePage = "<!DOCTYPE html>\n<html><body>Checking your browser body-marker-9902</body></html>"

var (
	leGuin  = source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}
	kindred = source.Ref{Source: source.ZLibrary, ID: "7731", Hash: "ff00ee"}
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serve starts a TLS server for h and returns a Searcher pointed at it plus
// the number of requests the server has seen.
func serve(t *testing.T, session bool, h http.HandlerFunc) (*Searcher, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	// fetch has no transport option; New copies http.DefaultTransport, so swap it to trust the test certificate.
	saved := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	client := fetch.New(fetch.WithInterval(0))
	http.DefaultTransport = saved
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(client, base, session)
	if err != nil {
		t.Fatal(err)
	}
	return s, calls
}

func reply(status int, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func TestNewRejectsNonHTTPS(t *testing.T) {
	for _, raw := range []string{"http://books.example", "ftp://books.example", "https://"} {
		base, _ := url.Parse(raw)
		if _, err := New(fetch.New(), base, false); err == nil {
			t.Errorf("New(%q) = nil error", raw)
		}
	}
	if _, err := New(fetch.New(), nil, false); err == nil {
		t.Error("New(nil) = nil error")
	}
}

func TestSearchFormAndRefs(t *testing.T) {
	var method, path string
	var form url.Values
	s, _ := serve(t, false, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = w.Write(fixture(t, "search.json"))
	})
	books, err := s.Search(context.Background(), "left hand", 2)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/eapi/book/search" {
		t.Fatalf("request = %s %s", method, path)
	}
	if form.Get("message") != "left hand" || form.Get("page") != "2" || form.Get("limit") != "25" {
		t.Fatalf("form = %v", form)
	}
	want := []source.Book{
		{Ref: leGuin, Title: "The Left Hand of Darkness", Author: "Ursula K. Le Guin", Year: "1969", Format: "epub", Size: "1.20 MB", Publisher: "Ace Books", Language: "english", ISBN: "9780441478125"},
		{Ref: kindred, Title: "Kindred", Author: "Octavia E. Butler", Year: "1979", Format: "PDF", Size: "3.05 MB", Language: "english"},
	}
	if len(books) != len(want) {
		t.Fatalf("books = %+v", books)
	}
	for i := range want {
		if books[i] != want[i] {
			t.Errorf("book %d = %+v, want %+v", i, books[i], want[i])
		}
		if again, err := source.Parse(books[i].Ref.String()); err != nil || again != want[i].Ref {
			t.Errorf("round trip %d = %+v, %v", i, again, err)
		}
	}
}

func TestSearchPageStartsAtOne(t *testing.T) {
	var page string
	s, _ := serve(t, false, func(w http.ResponseWriter, r *http.Request) {
		page = r.FormValue("page")
		_, _ = w.Write(fixture(t, "search-empty.json"))
	})
	books, err := s.Search(context.Background(), "nothing", 0)
	if err != nil || len(books) != 0 {
		t.Fatalf("Search = %v, %v; want empty, nil", books, err)
	}
	if page != "1" {
		t.Fatalf("page = %q, want 1", page)
	}
}

func TestSearchAmbiguousRefRefused(t *testing.T) {
	s, _ := serve(t, false, reply(200, fixture(t, "search-no-hash.json")))
	books, err := s.Search(context.Background(), "x", 1)
	var malformed *ErrMalformed
	if !errors.As(err, &malformed) || malformed.Field != "books[0].hash" || books != nil {
		t.Fatalf("Search = %v, %v; want ErrMalformed books[0].hash", books, err)
	}
}

func TestDetailPathAndFields(t *testing.T) {
	var path string
	s, _ := serve(t, false, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = w.Write(fixture(t, "detail.json"))
	})
	got, err := s.Detail(context.Background(), leGuin)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/eapi/book/5512/a1b2c3" {
		t.Fatalf("path = %q", path)
	}
	want := source.Detail{
		Ref: leGuin, Title: "The Left Hand of Darkness", Author: "Ursula K. Le Guin", Publisher: "Ace Books",
		Year: "1969", Language: "english", Format: "epub", Size: "1.20 MB", ISBN: "9780441478125", Pages: "304",
		Description: "A lone envoy visits Gethen.\n\nWinter & ice.", HasDownload: true,
	}
	if got != want {
		t.Fatalf("Detail = %+v\nwant %+v", got, want)
	}
}

func TestDetailFilesizeFallback(t *testing.T) {
	s, _ := serve(t, false, reply(200, fixture(t, "detail-filesize.json")))
	got, err := s.Detail(context.Background(), kindred)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != "3198156" || got.HasDownload || got.ISBN != "" {
		t.Fatalf("Detail = %+v", got)
	}
}

func TestDetailBadRef(t *testing.T) {
	s, calls := serve(t, false, reply(200, fixture(t, "detail.json")))
	for _, ref := range []source.Ref{
		{Source: source.Anna, ID: "5512"},
		{Source: source.ZLibrary, ID: "5512"},
		{Source: source.ZLibrary, ID: "5512", Hash: "a/b"},
		{},
	} {
		if _, err := s.Detail(context.Background(), ref); !errors.Is(err, ErrBadRef) {
			t.Errorf("Detail(%+v) error = %v, want ErrBadRef", ref, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("server saw %d requests, want 0", calls.Load())
	}
}

func TestMalformedResponses(t *testing.T) {
	tests := []struct {
		name, field string
		body        []byte
	}{
		{"not json", "body", []byte("oops body-marker-1188")},
		{"no book", "book", fixture(t, "detail-no-book.json")},
		{"no success", "success", []byte(`{"book":{"title":"x"}}`)},
		{"object title", "body", []byte(`{"success":1,"book":{"title":{"x":1}}}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := serve(t, false, reply(200, tc.body))
			_, err := s.Detail(context.Background(), leGuin)
			var malformed *ErrMalformed
			if !errors.As(err, &malformed) || malformed.Field != tc.field {
				t.Fatalf("error = %v, want ErrMalformed %s", err, tc.field)
			}
			if strings.Contains(err.Error(), "body-marker") {
				t.Fatalf("error leaks body: %v", err)
			}
		})
	}
}

func TestSessionExpired(t *testing.T) {
	for _, tc := range []struct{ file, msg string }{
		{"error-login.json", "Please login"},
		{"error-credentials.json", "Invalid credentials"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			s, _ := serve(t, true, reply(400, fixture(t, tc.file)))
			_, err := s.Search(context.Background(), "x", 1)
			if !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("error = %v, want ErrSessionExpired", err)
			}
			if !strings.Contains(err.Error(), tc.msg) || strings.Contains(err.Error(), "body-marker") {
				t.Fatalf("error = %q", err)
			}
		})
	}
}

func TestChallenge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"513", 513, `{"success":0}`},
		{"517", 517, ""},
		{"html 200", 200, challengePage},
		{"html 403", 403, challengePage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := serve(t, false, reply(tc.status, []byte(tc.body)))
			_, err := s.Detail(context.Background(), leGuin)
			if !errors.Is(err, ErrChallenged) {
				t.Fatalf("error = %v, want ErrChallenged", err)
			}
			if msg := err.Error(); !strings.Contains(msg, "challenging automated requests") || strings.Contains(msg, "retry") || strings.Contains(msg, "body-marker") {
				t.Fatalf("error = %q", msg)
			}
		})
	}
}

// Remote text must not carry control characters into the terminal.
func TestDecodeSearchStripsControlCharacters(t *testing.T) {
	body := `{"success":1,"books":[{"id":"5512","hash":"a1b2c3","title":"T\u001b[2Jitle","author":"A\nB","year":"19\u001b69","extension":"e\u001bpub","filesizeString":"1\u000bMB","publisher":"P\tQ","language":"en","isbn":"97\u001b78"}]}`
	books, err := decodeSearch(&fetch.Response{StatusCode: http.StatusOK, Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	b := books[0]
	for _, s := range []string{b.Title, b.Author, b.Year, b.Format, b.Size, b.Publisher, b.Language, b.ISBN} {
		if i := strings.IndexFunc(s, unicode.IsControl); i >= 0 {
			t.Errorf("%q carries control rune %q", s, s[i])
		}
	}
}

func TestDetailStripsControlCharacters(t *testing.T) {
	body := `{"success":1,"book":{"id":"5512","hash":"a1b2c3","title":"T\u001b[2Jitle","author":"A\nB","description":"D\u001b[2Jer\nEnd","dl":"/dl/5512/a1b2c3"}}`
	s, _ := serve(t, false, reply(http.StatusOK, []byte(body)))
	d, err := s.Detail(context.Background(), leGuin)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{d.Title, d.Author, d.Description} {
		if i := strings.IndexFunc(s, unicode.IsControl); i >= 0 {
			t.Errorf("%q carries control rune %q", s, s[i])
		}
	}
}

func TestResponseError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		want   ResponseError
	}{
		{"404 envelope", 404, fixture(t, "error-not-found.json"), ResponseError{404, "Book not found"}},
		{"200 success 0", 200, fixture(t, "error-not-found.json"), ResponseError{200, "Book not found"}},
		{"400 plain", 400, []byte("bad request body-marker-3"), ResponseError{400, ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := serve(t, false, reply(tc.status, tc.body))
			_, err := s.Detail(context.Background(), leGuin)
			var re *ResponseError
			if !errors.As(err, &re) || *re != tc.want {
				t.Fatalf("error = %v, want %+v", err, tc.want)
			}
			if strings.Contains(err.Error(), "body-marker") {
				t.Fatalf("error leaks body: %v", err)
			}
		})
	}
}

func cacheFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return len(entries)
}

func TestCacheAnonymousHitAndMiss(t *testing.T) {
	s, calls := serve(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eapi/book/search" {
			_, _ = w.Write(fixture(t, "search.json"))
			return
		}
		_, _ = w.Write(fixture(t, "detail.json"))
	})
	s.Store = cache.Open(t.TempDir())
	for range 2 {
		if books, err := s.Search(context.Background(), "Left  Hand", 1); err != nil || len(books) != 2 {
			t.Fatalf("Search = %v, %v", books, err)
		}
		if d, err := s.Detail(context.Background(), leGuin); err != nil || d.Title == "" {
			t.Fatalf("Detail = %+v, %v", d, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("server saw %d requests, want 2", calls.Load())
	}
	if _, ok := s.Store.Get(cache.SearchKey(source.ZLibrary, "left hand", 1)); !ok {
		t.Error("search not stored under the zlibrary key")
	}
	if _, ok := s.Store.Get(cache.RefKey(leGuin)); !ok {
		t.Error("detail not stored under the ref key")
	}
}

func TestCacheIgnoresOtherSource(t *testing.T) {
	s, calls := serve(t, false, reply(200, fixture(t, "search.json")))
	s.Store = cache.Open(t.TempDir())
	anna := cache.Entry{Key: cache.SearchKey(source.Anna, "kindred", 1), Status: 200, FetchedAt: time.Now(), Body: fixture(t, "search-empty.json")}
	if err := s.Store.Put(anna); err != nil {
		t.Fatal(err)
	}
	books, err := s.Search(context.Background(), "kindred", 1)
	if err != nil || len(books) != 2 || calls.Load() != 1 {
		t.Fatalf("Search = %d books, %v, %d calls; want live zlibrary result", len(books), err, calls.Load())
	}
}

func TestCacheStaleFallback(t *testing.T) {
	s, _ := serve(t, false, reply(404, fixture(t, "error-not-found.json")))
	s.Store = cache.Open(t.TempDir())
	old := cache.Entry{Key: cache.SearchKey(source.ZLibrary, "kindred", 1), Status: 200, FetchedAt: time.Now().Add(-2 * cache.DefaultTTL), Body: fixture(t, "search.json")}
	if err := s.Store.Put(old); err != nil {
		t.Fatal(err)
	}
	books, err := s.Search(context.Background(), "kindred", 1)
	var re *ResponseError
	if len(books) != 2 || !errors.As(err, &re) {
		t.Fatalf("Search = %d books, %v; want stale books with the live error", len(books), err)
	}
}

func TestCacheSessionNeverReadsOrWrites(t *testing.T) {
	s, calls := serve(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eapi/book/search" {
			_, _ = w.Write(fixture(t, "search.json"))
			return
		}
		_, _ = w.Write(fixture(t, "detail.json"))
	})
	dir := t.TempDir()
	s.Store = cache.Open(dir)
	// Fresh entries that would be served if the store were consulted.
	for _, e := range []cache.Entry{
		{Key: cache.SearchKey(source.ZLibrary, "kindred", 1), Status: 200, FetchedAt: time.Now(), Body: fixture(t, "search-empty.json")},
		{Key: cache.RefKey(leGuin), Status: 200, FetchedAt: time.Now(), Body: fixture(t, "detail-filesize.json")},
	} {
		if err := s.Store.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	before := cacheFiles(t, dir)
	books, err := s.Search(context.Background(), "kindred", 1)
	if err != nil || len(books) != 2 {
		t.Fatalf("Search = %v, %v; want live result", books, err)
	}
	if _, err := s.Search(context.Background(), "other", 1); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(context.Background(), leGuin)
	if err != nil || d.Size != "1.20 MB" {
		t.Fatalf("Detail = %+v, %v; want live result", d, err)
	}
	if calls.Load() != 3 || cacheFiles(t, dir) != before {
		t.Fatalf("calls = %d, files %d -> %d; want 3 live calls and no writes", calls.Load(), before, cacheFiles(t, dir))
	}
	if e, _ := s.Store.Get(cache.RefKey(leGuin)); string(e.Body) != string(fixture(t, "detail-filesize.json")) {
		t.Fatal("session detail overwrote the cached entry")
	}
}

func TestCacheSkipsEmptySearch(t *testing.T) {
	s, calls := serve(t, false, reply(200, fixture(t, "search-empty.json")))
	dir := t.TempDir()
	s.Store = cache.Open(dir)
	for range 2 {
		if books, err := s.Search(context.Background(), "nothing", 1); err != nil || len(books) != 0 {
			t.Fatalf("Search = %v, %v", books, err)
		}
	}
	if calls.Load() != 2 || cacheFiles(t, dir) != 0 {
		t.Fatalf("calls = %d, cache files = %d; want 2 live calls and no writes", calls.Load(), cacheFiles(t, dir))
	}
}

func TestUnavailableNamesOnlyHost(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.Close() // every request is now refused
	s, err := New(fetch.New(fetch.WithInterval(0)), base, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	calls := map[string]func() error{
		"Search": func() error { _, err := s.Search(ctx, "secret-query-77", 1); return err },
		"Detail": func() error { _, err := s.Detail(ctx, leGuin); return err },
		"ResolveDownload": func() error {
			_, err := s.ResolveDownload(ctx, leGuin)
			return err
		},
	}
	for name, call := range calls {
		err := call()
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s = %v, want ErrUnavailable", name, err)
		}
		if msg := err.Error(); msg != ErrUnavailable.Error()+": "+base.Host {
			t.Errorf("%s message = %q, want host only", name, msg)
		}
	}
}

func TestUnavailableOnDeadline(t *testing.T) {
	s, _ := serve(t, false, reply(200, fixture(t, "search.json")))
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	if _, err := s.Search(ctx, "x", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Search = %v, want ErrUnavailable", err)
	}
}

func TestCancelStaysCanceled(t *testing.T) {
	s, _ := serve(t, false, reply(200, fixture(t, "search.json")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Search(ctx, "x", 1)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("Search = %v, want context.Canceled", err)
	}
}

func TestParagraphs(t *testing.T) {
	in := "<p>One <b>bold</b>\nline &amp; more.</p>\n<p>Two.<br/>Three\x1b<BR></p>"
	if got, want := paragraphs(in), "One bold line & more.\n\nTwo.\n\nThree"; got != want {
		t.Fatalf("paragraphs = %q, want %q", got, want)
	}
}
