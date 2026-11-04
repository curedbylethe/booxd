package parse

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestSearchHitPage(t *testing.T) {
	books, err := Search(strings.NewReader(string(fixture(t, "search-hit.html"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 20 {
		t.Fatalf("got %d books", len(books))
	}
	tests := []struct {
		index                                                  int
		id, slug, title, author, year, format, size, publisher string
	}{
		{0, "36451", "36451-test", "Test", "", "2022", "PDF", "51.0 MB", ""},
		{2, "30208739", "30208739-test-1", "Test", "Sleator, William", "2010", "EPUB", "3.0 MB", "Amulet Books"},
		{10, "42987114", "test-42987114", "Test", "Marie-Laure", "2015", "", "1 B", "Blurb, Incorporated"},
		{16, "35840679", "test-scient-e-farm-eserciz", "TEST - SCIENT. E FARM. ESERCIZ", "", "", "", "1 B", ""},
		{19, "29208400", "test-erzahlungen", "TEST [Erzählungen]", "Lem, Stanislaw", "", "PDF", "1.1 MB", ""},
	}
	for _, tt := range tests {
		b := books[tt.index]
		if b.ID != tt.id || b.Slug != tt.slug || b.Title != tt.title || b.Author != tt.author || b.Year != tt.year || b.Format != tt.format || b.Size != tt.size || b.Publisher != tt.publisher || b.Language != "" {
			t.Errorf("row %d: %+v", tt.index+1, b)
		}
	}
	if books[0].URL != "https://annas-archive.is/books/36451-36451-test" {
		t.Errorf("URL: %q", books[0].URL)
	}
}
func TestSearchCountMismatch(t *testing.T) {
	s := strings.ReplaceAll(string(fixture(t, "search-hit.html")), "bg-white rounded-lg shadow p-4 mb-4", "rounded-lg shadow p-4 mb-4")
	_, err := Search(strings.NewReader(s))
	if err == nil || !strings.Contains(err.Error(), "parsed 0 of 20") {
		t.Fatalf("error: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("reader failed") }
func TestSearchReaderError(t *testing.T) {
	if _, err := Search(errorReader{}); err == nil {
		t.Fatal("expected error")
	}
}
func TestSearchMinimalCard(t *testing.T) {
	books, err := Search(strings.NewReader(`<div class="bg-white rounded-lg shadow p-4 mb-4"><h3><a href="/books/1-one">One</a></h3></div>`))
	if err != nil || len(books) != 1 {
		t.Fatalf("books: %+v, error: %v", books, err)
	}
	if books[0] != (Book{ID: "1", Slug: "one", Title: "One", URL: "/books/1-one"}) {
		t.Fatalf("book: %+v", books[0])
	}
}
