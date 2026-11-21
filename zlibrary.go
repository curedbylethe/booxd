package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/down"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/curedbylethe/booxd/internal/source/zlibrary"
	"github.com/curedbylethe/booxd/internal/tui"
)

const sourceUsage = "book source: anna or zlibrary"

// parseSourceFlag accepts a --source value that names a known source.
func parseSourceFlag(raw string, stderr io.Writer) (source.Name, bool) {
	name := source.Name(raw)
	if name != source.Anna && name != source.ZLibrary && name != source.IRC {
		_, _ = fmt.Fprintf(stderr, "booxd: unknown source %q\n", raw)
		return "", false
	}
	return name, true
}

// annaOnly reports false, after naming the flag, when an Anna-only flag was set.
func annaOnly(flags *flag.FlagSet, stderr io.Writer, names ...string) bool {
	set := ""
	flags.Visit(func(f *flag.Flag) {
		for _, name := range names {
			if set == "" && f.Name == name {
				set = name
			}
		}
	})
	if set != "" {
		_, _ = fmt.Fprintf(stderr, "booxd: --%s applies to anna only\n", set)
		return false
	}
	return true
}

// zlibraryOnly reports false, after naming the flag, when a Z-Library-only
// filter flag was set on another source.
func zlibraryOnly(flags *flag.FlagSet, stderr io.Writer, names ...string) bool {
	set := ""
	flags.Visit(func(f *flag.Flag) {
		for _, name := range names {
			if set == "" && f.Name == name {
				set = name
			}
		}
	})
	if set != "" {
		_, _ = fmt.Fprintf(stderr, "booxd: --%s applies to zlibrary only\n", set)
		return false
	}
	return true
}

// originJar scopes a loaded cookie jar to the [zlibrary] base URL: the file's
// Domain field may name a whole dot-domain, and net/http's jar ignores ports,
// so the raw jar would leak session cookies to sibling hosts on redirects.
type originJar struct {
	jar  http.CookieJar
	base url.URL
}

func (o *originJar) sameOrigin(u *url.URL) bool {
	return u.Scheme == o.base.Scheme && strings.EqualFold(u.Hostname(), o.base.Hostname()) && originPort(u) == originPort(&o.base)
}

func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	if u.Scheme == "http" {
		return "80"
	}
	return ""
}

func (o *originJar) Cookies(u *url.URL) []*http.Cookie {
	if !o.sameOrigin(u) {
		return nil
	}
	return o.jar.Cookies(u)
}

func (o *originJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if !o.sameOrigin(u) {
		return
	}
	o.jar.SetCookies(u, cookies)
}

// zlibrarySession reads the [zlibrary] settings: the https base URL and, when
// session_file is set, a jar holding remix_userid or remix_userkey for that
// URL. A nil jar means anonymous. Every error is a missing prerequisite.
func zlibrarySession(cfg config.Config) (*url.URL, http.CookieJar, error) {
	if cfg.ZLibraryBaseURL == "" {
		return nil, nil, errors.New("zlibrary needs [zlibrary] base_url or BOOXD_ZLIBRARY_BASE_URL")
	}
	base, err := url.Parse(cfg.ZLibraryBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		// The value is not echoed: it is user configuration and may carry a query.
		return nil, nil, errors.New("[zlibrary] base_url (BOOXD_ZLIBRARY_BASE_URL) must be an https URL")
	}
	if cfg.ZLibrarySessionFile == "" {
		return base, nil, nil
	}
	jar, err := down.LoadJar(cfg.ZLibrarySessionFile)
	if err != nil {
		return nil, nil, err
	}
	scoped := &originJar{jar: jar, base: *base}
	for _, c := range scoped.Cookies(base) {
		if c.Name == "remix_userid" || c.Name == "remix_userkey" {
			return base, scoped, nil
		}
	}
	return nil, nil, fmt.Errorf("session file %s has no remix_userid or remix_userkey cookie for the [zlibrary] base_url host", cfg.ZLibrarySessionFile)
}

var errNeedSession = errors.New("downloads need a signed-in session; set [zlibrary] session_file or BOOXD_ZLIBRARY_SESSION_FILE")

// newZLibrary builds a Searcher and its client from cfg, and reports whether
// it is signed in. An error is a missing prerequisite (exit status 2); no
// client exists and no request has been sent at that point.
func newZLibrary(cfg config.Config, needSession bool, log io.Writer, newClient clientFunc) (*zlibrary.Searcher, *fetch.Client, bool, error) {
	base, jar, err := zlibrarySession(cfg)
	if err == nil && needSession && jar == nil {
		err = errNeedSession
	}
	if err != nil {
		return nil, nil, false, err
	}
	client := newClient(cfg, log, jar)
	s, err := zlibrary.New(client, base, jar != nil)
	if err != nil {
		return nil, nil, false, err
	}
	return s, client, jar != nil, nil
}

func zlibraryPrerequisite(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
	return 2
}

// zlibraryMessage is err as one line without a request URL: a transport
// error quotes the URL it failed on, and a download link carries a signed query.
func zlibraryMessage(err error) string {
	msg := err.Error()
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		host := "request"
		if u, parseErr := url.Parse(urlErr.URL); parseErr == nil && u.Host != "" {
			host = u.Host
		}
		msg = strings.ReplaceAll(msg, urlErr.Error(), urlErr.Op+" "+host+": "+urlErr.Err.Error())
	}
	if errors.Is(err, zlibrary.ErrSessionExpired) {
		msg += "; supply a fresh session file"
	}
	return msg
}

func zlibraryFailure(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "booxd: %s\n", zlibraryMessage(err))
	return 1
}

func searchZLibrary(cfg config.Config, store *cache.Store, query string, opts source.SearchOptions, log, stdout, stderr io.Writer, newClient clientFunc) int {
	s, _, session, err := newZLibrary(cfg, false, log, newClient)
	if err != nil {
		return zlibraryPrerequisite(stderr, err)
	}
	if !session {
		s.Store = store
	}
	books, err := s.Search(context.Background(), query, opts)
	if err != nil {
		if len(books) == 0 {
			return zlibraryFailure(stderr, err)
		}
		_, _ = fmt.Fprintf(stderr, "booxd: %s; showing cached results\n", zlibraryMessage(err))
	}
	if len(books) == 0 {
		_, _ = fmt.Fprintln(stderr, "no results")
		return 0
	}
	printBooks(stdout, toParseBooks(books))
	return 0
}

func showZLibrary(cfg config.Config, store *cache.Store, ref source.Ref, log, stdout, stderr io.Writer, newClient clientFunc) int {
	s, _, session, err := newZLibrary(cfg, false, log, newClient)
	if err != nil {
		return zlibraryPrerequisite(stderr, err)
	}
	if !session {
		s.Store = store
	}
	detail, err := s.Detail(context.Background(), ref)
	if err != nil {
		if detail.Ref.IsZero() {
			return zlibraryFailure(stderr, err)
		}
		_, _ = fmt.Fprintf(stderr, "booxd: %s; showing cached details\n", zlibraryMessage(err))
	}
	renderShow(stdout, toParseDetail(detail))
	return 0
}

func getZLibrary(cfg config.Config, ref source.Ref, open bool, log, stdout, stderr io.Writer, newClient clientFunc) int {
	s, client, _, err := newZLibrary(transferConfig(cfg), true, log, newClient)
	if err != nil {
		return zlibraryPrerequisite(stderr, err)
	}
	path, err := downloadZLibrary(context.Background(), s, client, cfg, ref, stderr, nil)
	if err != nil && !errors.Is(err, down.ErrExists) {
		return zlibraryFailure(stderr, err)
	}
	return finishGet(stdout, stderr, path, err, open)
}

// transferConfig is cfg for a client that downloads: as on the Anna path, the
// transfer has no whole-request timeout. downloadZLibrary gives the metadata
// requests the configured one.
func transferConfig(cfg config.Config) config.Config {
	cfg.RequestTimeout = 0
	return cfg
}

// downloadZLibrary resolves ref's link and saves the file, writing down's
// progress lines to progress and byte counts to bytes (nil ignores either).
// client must come from transferConfig. A file already present returns its
// path with down.ErrExists.
func downloadZLibrary(ctx context.Context, s *zlibrary.Searcher, client *fetch.Client, cfg config.Config, ref source.Ref, progress io.Writer, bytes func(done, total int64)) (string, error) {
	pageCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	detail, err := s.Detail(pageCtx, ref)
	var link url.URL
	if err == nil {
		link, err = s.ResolveDownload(pageCtx, ref)
	}
	cancel()
	if err != nil {
		return "", err
	}
	path, err := down.Download(ctx, client, down.Request{
		URL:      link.String(),
		Dir:      cfg.DownloadDir,
		Name:     zlibrary.SaveName(ref, detail.Format),
		Progress: progress,
		Bytes:    bytes,
	})
	// The link was just resolved, so these statuses mean the host expired or refused it.
	var status *down.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
			return "", fmt.Errorf("zlibrary: download link expired or refused (status %d); run get again", status.Code)
		}
	}
	return path, err
}

// tuiZLibrary serves the TUI's zlibrary callbacks from one client, so one
// request per host holds across search, detail and download. The client comes
// from transferConfig; search and detail carry the configured timeout. A
// missing prerequisite does not stop the TUI: each callback reports it.
type tuiZLibrary struct {
	cfg     config.Config
	s       *zlibrary.Searcher
	client  *fetch.Client
	session bool
	err     error
}

func newTUIZLibrary(cfg config.Config, store *cache.Store, newClient clientFunc) tuiZLibrary {
	s, client, session, err := newZLibrary(transferConfig(cfg), false, nil, newClient)
	if err == nil && !session {
		s.Store = store
	}
	return tuiZLibrary{cfg: cfg, s: s, client: client, session: session, err: err}
}

func (z tuiZLibrary) search(ctx context.Context, query string, page int) ([]parse.Book, error) {
	if z.err != nil {
		return nil, z.err
	}
	ctx, cancel := context.WithTimeout(ctx, z.cfg.RequestTimeout)
	defer cancel()
	books, err := z.s.Search(ctx, query, source.SearchOptions{Page: page})
	if err != nil {
		return nil, errors.New(zlibraryMessage(err))
	}
	return toParseBooks(books), nil
}

func (z tuiZLibrary) detail(ctx context.Context, ref source.Ref) (parse.Detail, error) {
	if z.err != nil {
		return parse.Detail{}, z.err
	}
	ctx, cancel := context.WithTimeout(ctx, z.cfg.RequestTimeout)
	defer cancel()
	detail, err := z.s.Detail(ctx, ref)
	if err != nil {
		return parse.Detail{}, errors.New(zlibraryMessage(err))
	}
	return toParseDetail(detail), nil
}

// download maps the shared download path's errors to the TUI's outcomes. Only
// prerequisite messages, which hold no link or cookie, reach the screen.
func (z tuiZLibrary) download(ctx context.Context, ref source.Ref, progress func(done, total int64)) (string, error) {
	err := z.err
	if err == nil && !z.session {
		err = errNeedSession
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", tui.ErrFailed, err)
	}
	path, err := downloadZLibrary(ctx, z.s, z.client, z.cfg, ref, nil, progress)
	switch {
	case err == nil:
		return path, nil
	case errors.Is(err, down.ErrExists):
		return path, tui.ErrExists
	case errors.Is(err, zlibrary.ErrSessionExpired):
		return "", tui.ErrSessionExpired
	case errors.Is(err, zlibrary.ErrQuota):
		return "", tui.ErrQuota
	case errors.Is(err, context.Canceled):
		// A cancelled transfer arrives as a *url.Error that quotes the link.
		return "", context.Canceled
	}
	return "", err
}

// toParseBooks and toParseDetail are the only code that knows both the
// provider-neutral source model and the parse model the printers take.
func toParseBooks(books []source.Book) []parse.Book {
	out := make([]parse.Book, len(books))
	for i, b := range books {
		out[i] = parse.Book{ID: b.Ref, Title: b.Title, Author: b.Author, Year: b.Year, Format: b.Format, Size: b.Size, Publisher: b.Publisher, Language: b.Language}
	}
	return out
}

func toParseDetail(d source.Detail) parse.Detail {
	out := parse.Detail{ID: d.Ref, Title: d.Title, Author: d.Author, Publisher: d.Publisher, Year: d.Year, Language: d.Language, Format: d.Format, Size: d.Size, ISBN: d.ISBN, Pages: d.Pages, Description: d.Description}
	if d.HasDownload {
		// Every zlibrary download needs a session file, signed in or not.
		out.Downloads = []parse.Download{{Label: string(source.ZLibrary), RequiresSession: true}}
	}
	return out
}
