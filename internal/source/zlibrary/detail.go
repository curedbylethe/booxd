package zlibrary

import (
	"context"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

type detailResponse struct {
	envelope
	Book *struct {
		Title       text `json:"title"`
		Author      text `json:"author"`
		Publisher   text `json:"publisher"`
		Year        text `json:"year"`
		Language    text `json:"language"`
		Extension   text `json:"extension"`
		Size        text `json:"filesizeString"`
		Bytes       text `json:"filesize"`
		ISBN        text `json:"isbn"`
		Pages       text `json:"pages"`
		Description text `json:"description"`
		Download    text `json:"dl"`
	} `json:"book"`
}

// Detail returns the record for ref, which must be a zlibrary ref with a hash.
// A stale cached record is returned together with a live failure.
func (s *Searcher) Detail(ctx context.Context, ref source.Ref) (source.Detail, error) {
	u, err := s.bookURL(ref)
	if err != nil {
		return source.Detail{}, err
	}
	send := func() (*fetch.Response, error) { return s.client.Get(ctx, u) }
	decodeDetail := func(resp *fetch.Response) (source.Detail, error) {
		var r detailResponse
		if err := decode(resp, &r); err != nil {
			return source.Detail{}, err
		}
		b := r.Book
		if b == nil {
			return source.Detail{}, &ErrMalformed{Field: "book"}
		}
		size := b.Size
		if size == "" {
			size = b.Bytes
		}
		return source.Detail{
			Ref:         ref,
			Title:       string(b.Title),
			Author:      string(b.Author),
			Publisher:   string(b.Publisher),
			Year:        string(b.Year),
			Language:    string(b.Language),
			Format:      string(b.Extension),
			Size:        string(size),
			ISBN:        string(b.ISBN),
			Pages:       string(b.Pages),
			Description: string(b.Description),
			HasDownload: b.Download != "",
		}, nil
	}
	return load(s, cache.RefKey(ref), ref.String(), 0, send, decodeDetail, nil)
}
