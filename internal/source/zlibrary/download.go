package zlibrary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

type fileResponse struct {
	envelope
	File *struct {
		Link     text `json:"downloadLink"`
		Allow    text `json:"allowDownload"`
		Disallow text `json:"disallowDownloadMessage"`
	} `json:"file"`
}

// ResolveDownload asks the file endpoint for ref's current download link. The
// link is expiring and signed: it is never cached, and no error carries it.
// A link on another host is allowed; one that is not https is refused.
func (s *Searcher) ResolveDownload(ctx context.Context, ref source.Ref) (url.URL, error) {
	u, err := s.bookURL(ref)
	if err != nil {
		return url.URL{}, err
	}
	// Open, unlike Get, never consults a cache lookup.
	stream, err := s.client.Open(ctx, u+"/file", nil)
	if err != nil {
		return url.URL{}, challenged(err)
	}
	body, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil {
		return url.URL{}, err
	}
	var r fileResponse
	err = decode(&fetch.Response{StatusCode: stream.StatusCode, Body: body}, &r)
	var re *ResponseError
	if errors.As(err, &re) && strings.Contains(strings.ToLower(re.Message), "daily download limit") {
		return url.URL{}, fmt.Errorf("%w: %s", ErrQuota, re.Message)
	}
	if err != nil {
		return url.URL{}, err
	}
	f := r.File
	if f == nil {
		return url.URL{}, &ErrMalformed{Field: "file"}
	}
	if f.Allow == "false" || f.Allow == "0" || f.Link == "" && f.Disallow != "" {
		if msg := oneLine(string(f.Disallow)); msg != "" {
			return url.URL{}, fmt.Errorf("%w: %s", ErrQuota, msg)
		}
		return url.URL{}, ErrQuota
	}
	if f.Link == "" {
		return url.URL{}, &ErrMalformed{Field: "file.downloadLink"}
	}
	// url.Parse errors quote the input, so they are replaced, not wrapped.
	link, err := s.base.Parse(string(f.Link))
	if err != nil {
		return url.URL{}, &ErrMalformed{Field: "file.downloadLink"}
	}
	if link.Scheme != "https" || link.Host == "" {
		return url.URL{}, errors.New("zlibrary: download link is not https")
	}
	return *link, nil
}

// SaveName is the fallback file name for ref: its id plus the lower-cased
// format, matching the Anna path. The downloader prefers Content-Disposition.
func SaveName(ref source.Ref, format string) string {
	format = strings.TrimPrefix(strings.ToLower(format), ".")
	if format == "" {
		return ref.ID
	}
	return ref.ID + "." + format
}
