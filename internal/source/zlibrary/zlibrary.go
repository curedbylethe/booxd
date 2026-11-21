// Package zlibrary turns the Z-Library EAPI's JSON into source-qualified
// books and details. It takes its base URL from the caller, compiles in no
// host, reads no configuration, and sends every request through
// internal/fetch.
package zlibrary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/source"
)

// Failure classes. Callers map them to messages and exit codes.
var (
	ErrBadRef         = errors.New("zlibrary: reference is not a zlibrary id and hash")
	ErrSessionExpired = errors.New("zlibrary: session rejected")
	ErrQuota          = errors.New("zlibrary: download quota exhausted")
	ErrChallenged     = errors.New("zlibrary: host is challenging automated requests")
)

// ErrMalformed reports a response that is not the expected JSON. It names the
// field and never carries the body.
type ErrMalformed struct{ Field string }

func (e *ErrMalformed) Error() string { return "zlibrary: malformed response: " + e.Field }

// ResponseError is any other refusal. Message is the envelope's own error
// text, reduced to one line; the body itself is never kept.
type ResponseError struct {
	Status  int
	Message string
}

func (e *ResponseError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("zlibrary: status %d", e.Status)
	}
	return fmt.Sprintf("zlibrary: status %d: %s", e.Status, e.Message)
}

// Searcher talks to one EAPI base URL.
type Searcher struct {
	// Store, when set, caches anonymous search and detail responses. It is
	// never read or written when the Searcher has a session.
	Store *cache.Store

	client  *fetch.Client
	base    *url.URL
	session bool
}

// New returns a Searcher for baseURL, which must be https. session reports
// whether client carries a user session; it disables the cache.
func New(client *fetch.Client, baseURL *url.URL, session bool) (*Searcher, error) {
	if baseURL == nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return nil, errors.New("zlibrary: base URL must be an https URL")
	}
	return &Searcher{client: client, base: baseURL, session: session}, nil
}

// bookURL returns /eapi/book/{id}/{hash} under the base URL, or ErrBadRef.
func (s *Searcher) bookURL(ref source.Ref) (string, error) {
	if ref.Source != source.ZLibrary {
		return "", fmt.Errorf("%w: source %q", ErrBadRef, ref.Source)
	}
	if err := ref.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrBadRef, err)
	}
	return s.base.JoinPath("eapi", "book", url.PathEscape(ref.ID), url.PathEscape(ref.Hash)).String(), nil
}

// load returns a fresh cached value for key, else the live one, caching it.
// A failed live request falls back to a stale entry and returns it together
// with the error, as the Anna search path does. With a session the store is
// not touched. keep, when set, decides whether a live value is stored.
func load[T any](s *Searcher, key, subject string, page int, send func() (*fetch.Response, error), decodeAs func(*fetch.Response) (T, error), keep func(T) bool) (T, error) {
	useCache := s.Store != nil && !s.session
	var entry cache.Entry
	hit := false
	if useCache {
		entry, hit = s.Store.Get(key)
		hit = hit && entry.Status == http.StatusOK
	}
	cached := &fetch.Response{StatusCode: entry.Status, Body: entry.Body}
	if age := time.Since(entry.FetchedAt); hit && age >= 0 && age < cache.DefaultTTL {
		if v, err := decodeAs(cached); err == nil {
			return v, nil
		}
	}
	resp, err := send()
	var v T
	if err == nil {
		v, err = decodeAs(resp)
	} else {
		err = challenged(err)
	}
	if err != nil {
		if hit {
			if stale, staleErr := decodeAs(cached); staleErr == nil {
				return stale, err
			}
		}
		var zero T
		return zero, err
	}
	if useCache && resp.StatusCode == http.StatusOK && (keep == nil || keep(v)) {
		_ = s.Store.Put(cache.Entry{Key: key, Query: subject, Page: page, Status: resp.StatusCode, FetchedAt: time.Now(), Body: resp.Body})
	}
	return v, nil
}

// challenged turns fetch's error for a wall status into ErrChallenged.
func challenged(err error) error {
	var status *fetch.StatusError
	if errors.As(err, &status) && (status.Code == 513 || status.Code == 517) {
		return fmt.Errorf("%w (status %d)", ErrChallenged, status.Code)
	}
	return err
}

// envelope is the part every EAPI response shares.
type envelope struct {
	Success text            `json:"success"`
	Error   json.RawMessage `json:"error"`
}

func (e *envelope) env() *envelope { return e }

// message returns the error text, which is a string or an object with message.
func (e *envelope) message() string {
	var s string
	if json.Unmarshal(e.Error, &s) != nil {
		var obj struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(e.Error, &obj)
		s = obj.Message
	}
	return oneLine(s)
}

// decode classifies resp and, on success, fills v.
func decode(resp *fetch.Response, v interface{ env() *envelope }) error {
	ok := resp.StatusCode/100 == 2
	if json.Unmarshal(resp.Body, v) != nil {
		if bytes.HasPrefix(bytes.TrimSpace(resp.Body), []byte("<")) {
			return fmt.Errorf("%w (status %d)", ErrChallenged, resp.StatusCode)
		}
		if !ok {
			return &ResponseError{Status: resp.StatusCode}
		}
		return &ErrMalformed{Field: "body"}
	}
	e := v.env()
	if ok && (e.Success == "1" || e.Success == "true") {
		return nil
	}
	if ok && e.Success == "" {
		return &ErrMalformed{Field: "success"}
	}
	msg := e.message()
	if strings.Contains(msg, "Invalid credentials") || strings.Contains(msg, "Please login") {
		return fmt.Errorf("%w: %s", ErrSessionExpired, msg)
	}
	return &ResponseError{Status: resp.StatusCode, Message: msg}
}

// text is a JSON scalar read as a string: the EAPI sends ids, years and
// flags as strings or numbers interchangeably.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	switch {
	case bytes.Equal(b, []byte("null")):
		*t = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = text(s)
	case b[0] == '{' || b[0] == '[':
		return errors.New("zlibrary: expected a JSON scalar")
	default:
		*t = text(b)
	}
	return nil
}

var tag = regexp.MustCompile(`<[^>]*>`)

// oneLine strips HTML tags and entities and collapses whitespace.
func oneLine(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tag.ReplaceAllString(s, " "))), " ")
}
