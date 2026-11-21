package zlibrary

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

// pageSize is the result count requested per search page.
const pageSize = 25

type searchResponse struct {
	envelope
	Books []struct {
		ID        text `json:"id"`
		Hash      text `json:"hash"`
		Title     text `json:"title"`
		Author    text `json:"author"`
		Year      text `json:"year"`
		Publisher text `json:"publisher"`
		Language  text `json:"language"`
		Extension text `json:"extension"`
		Size      text `json:"filesizeString"`
		ISBN      text `json:"isbn"`
	} `json:"books"`
}

// Search returns one 1-based page of results for query. A success with no
// books is an empty result. When a live request fails and a stale cached page
// exists, Search returns that page together with the error.
func (s *Searcher) Search(ctx context.Context, query string, page int) ([]source.Book, error) {
	if page < 1 {
		page = 1
	}
	form := url.Values{"message": {query}, "page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(pageSize)}}
	send := func() (*fetch.Response, error) {
		return s.client.Post(ctx, s.base.JoinPath("eapi", "book", "search").String(), form, nil)
	}
	// Like the Anna path, an empty page is not cached.
	nonEmpty := func(books []source.Book) bool { return len(books) > 0 }
	return load(s, cache.SearchKey(source.ZLibrary, query, page), query, page, send, decodeSearch, nonEmpty)
}

func decodeSearch(resp *fetch.Response) ([]source.Book, error) {
	var r searchResponse
	if err := decode(resp, &r); err != nil {
		return nil, err
	}
	books := make([]source.Book, 0, len(r.Books))
	for i, b := range r.Books {
		ref := source.Ref{Source: source.ZLibrary, ID: string(b.ID), Hash: string(b.Hash)}
		switch {
		case ref.ID == "":
			return nil, &ErrMalformed{Field: fmt.Sprintf("books[%d].id", i)}
		case ref.Hash == "":
			return nil, &ErrMalformed{Field: fmt.Sprintf("books[%d].hash", i)}
		case ref.Validate() != nil:
			// A ref that cannot be parsed back would not round-trip.
			return nil, &ErrMalformed{Field: fmt.Sprintf("books[%d].id", i)}
		}
		books = append(books, source.Book{
			Ref:       ref,
			Title:     string(b.Title),
			Author:    string(b.Author),
			Year:      string(b.Year),
			Format:    string(b.Extension),
			Size:      string(b.Size),
			Publisher: string(b.Publisher),
			Language:  string(b.Language),
			ISBN:      string(b.ISBN),
		})
	}
	return books, nil
}
