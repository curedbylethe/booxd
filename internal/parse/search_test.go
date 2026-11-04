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

func TestSearchMissPage(t *testing.T) {
	books, err := Search(strings.NewReader(string(fixture(t, "search-miss.html"))))
	if err != nil || len(books) != 0 {
		t.Fatalf("books: %+v, error: %v", books, err)
	}
}

func TestSearchNonLatinPage(t *testing.T) {
	books, err := Search(strings.NewReader(string(fixture(t, "search-nonlatin.html"))))
	if err != nil || len(books) != 9 {
		t.Fatalf("got %d books, error: %v", len(books), err)
	}
	for _, tt := range []struct {
		index                                            int
		id, title, author, year, format, size, publisher string
	}{
		{0, "40695905", "Prestuplenie i nakazanie. V 2 t.", "F. M. Dostoevski", "2008", "", "1 B", "Intreid Korporeishn"},
		{1, "31387360", "Преступление и наказание", "Достоевский Ф.М.", "", "DJVU", "2.7 MB", ""},
		{4, "7812190", "Преступление и наказание (Russian Edition)", "", "2015", "EPUB", "1 B", `Издательство "Проспект"`},
	} {
		b := books[tt.index]
		if b.ID != tt.id || b.Title != tt.title || b.Author != tt.author || b.Year != tt.year || b.Format != tt.format || b.Size != tt.size || b.Publisher != tt.publisher {
			t.Errorf("row %d: %+v", tt.index+1, b)
		}
	}
	if books[4].Slug != "-russian-edition-7812190" {
		t.Errorf("row 5 slug: %q", books[4].Slug)
	}
}

func TestSearchPage2(t *testing.T) {
	books, err := Search(strings.NewReader(string(fixture(t, "search-page2.html"))))
	if err != nil || len(books) != 20 {
		t.Fatalf("got %d books, error: %v", len(books), err)
	}
	first := books[0]
	if first.ID != "29764399" || first.Title != "TEST [Erzählungen]" || first.Author != "Lem, Stanislaw" || first.Year != "2011" || first.Format != "PDF" || first.Size != "1.1 MB" {
		t.Errorf("first row: %+v", first)
	}
	last := books[19]
	if last.ID != "34183821" || last.Slug != "test-ammissione-medicina-ondontoiatria-veterina" || last.Title != "Test ammissione. Medicina, ondontoiatria, veterinaria, professioni sanitarie. Teoria e quiz dell'esercitatore... Manuale completo per la preparazione ai nuovi test.." || last.Author != "" || last.Year != "2014" || last.Format != "" || last.Size != "1 B" || last.Publisher != "Edizioni Giuridiche Simone" {
		t.Errorf("last row: %+v", last)
	}
}

func TestSearchGarbage(t *testing.T) {
	books, err := Search(strings.NewReader("<html><body>not a search page</body></html>"))
	if err != nil || len(books) != 0 {
		t.Fatalf("books: %+v, error: %v", books, err)
	}
}

func TestSearchEmptyInput(t *testing.T) {
	books, err := Search(strings.NewReader(""))
	if err != nil || len(books) != 0 {
		t.Fatalf("books: %+v, error: %v", books, err)
	}
}

func TestSearchCardWithoutFields(t *testing.T) {
	books, err := Search(strings.NewReader(`<div class="bg-white rounded-lg shadow p-4 mb-4"></div>`))
	if err != nil || len(books) != 1 || books[0] != (Book{}) {
		t.Fatalf("books: %+v, error: %v", books, err)
	}
}
