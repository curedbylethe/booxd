package irc

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// TestRoundTrip proves a Search result can be fetched end to end through one
// fake Client. Search yields a two-book archive; the first book's request line
// is re-sent and its body streams to a .part file and is renamed into place.
// Both offers arrive over net.Pipe; no network is touched.
func TestRoundTrip(t *testing.T) {
	f := newFakeClient("#ebooks")

	results := zipFixture(t, lineA+"\n"+lineB+"\n")
	body := []byte("the complete book body served by the filebot")

	// The second Say is the fetch re-send: answer it with a body offer from
	// the book's server. The first Say, the search, needs no file offer.
	var target Book
	f.onSay = func(n int) {
		if n == 2 {
			f.send(dcc.Offer{From: target.Server, Name: "book.epub", Size: int64(len(body))})
		}
	}

	s := testSearcher(f)
	s.openOffer = roundtripOpen(results, body)

	// The search answer is offered before Search starts; its nick matches the
	// default SearchBot "search".
	f.send(dcc.Offer{From: "search", Name: "results.zip", Size: int64(len(results))})

	ctx := context.Background()
	books, err := s.Search(ctx, "jane austen")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if want := []Book{bookA, bookB}; !reflect.DeepEqual(books, want) {
		t.Fatalf("Search books = %+v, want %+v", books, want)
	}
	target = books[0]

	dst := filepath.Join(t.TempDir(), "book.epub")
	written, err := s.Fetch(ctx, books[0], dst)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if written != int64(len(body)) {
		t.Fatalf("written = %d, want %d", written, len(body))
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(body) {
		t.Fatalf("file %s = %q, error = %v; want %q", dst, got, err, body)
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatalf("stat %s.part error = %v, want not exist", dst, err)
	}

	texts, targets := f.waitSays(t, 2)
	if want := []string{"@search jane austen", books[0].requestLine()}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("Say texts = %q, want %q", texts, want)
	}
	if want := strings.TrimSpace(strings.Split(books[0].Raw, "::INFO::")[0]); texts[1] != want {
		t.Fatalf("second Say %q is not the raw line without the ::INFO:: tail (%q)", texts[1], want)
	}
	if want := []string{"#ebooks", "#ebooks"}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("Say targets = %q, want %q", targets, want)
	}
	if n := f.releaseCount(); n != 2 {
		t.Fatalf("ReleaseSearch calls = %d, want 2", n)
	}
}

// roundtripOpen serves the search archive for the results offer and the book
// body for the file offer, each over a net.Pipe whose writer closes after the
// payload. No listener or real dial is involved.
func roundtripOpen(results, body []byte) func(context.Context, dcc.Offer) (io.ReadCloser, error) {
	return func(_ context.Context, offer dcc.Offer) (io.ReadCloser, error) {
		data := results
		if offer.Name != "results.zip" {
			data = body
		}
		reader, writer := net.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			_, _ = writer.Write(data)
		}()
		return reader, nil
	}
}
