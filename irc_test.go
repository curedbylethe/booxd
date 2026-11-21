package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/config"
	"github.com/curedbylethe/booxd/internal/fetch"
	sourceirc "github.com/curedbylethe/booxd/internal/source/irc"
)

const (
	ircNotice = "irc: searching #ebooks (experimental source)\n"
	ircRow    = "irc:%21artemis_serv%2016d6770d2ba9%20%7C%20The%20Last%20Hero.pdf\tThe Last Hero\t\t\tpdf\t9.1 MB\n"
)

type fakeIRCSearcher struct {
	books      []sourceirc.Book
	err        error
	fetch      func(context.Context, sourceirc.Book, string) (int64, error)
	calls      int
	fetchCalls int
	fetched    sourceirc.Book
	dstPath    string
}

func (f *fakeIRCSearcher) Search(context.Context, string) ([]sourceirc.Book, error) {
	f.calls++
	return f.books, f.err
}

func (f *fakeIRCSearcher) Fetch(ctx context.Context, book sourceirc.Book, dstPath string) (int64, error) {
	f.fetchCalls++
	f.fetched, f.dstPath = book, dstPath
	if f.fetch != nil {
		return f.fetch(ctx, book, dstPath)
	}
	return 0, errors.New("unexpected Fetch")
}

type ircHook struct {
	searcher ircSearcher
	cleanup  func() error
	dialErr  error
	calls    int
}

// stubIRCSearcher swaps the dial hook for the duration of the test.
func stubIRCSearcher(t *testing.T, s ircSearcher, cleanup func() error, dialErr error) *ircHook {
	t.Helper()
	h := &ircHook{searcher: s, cleanup: cleanup, dialErr: dialErr}
	old := newIRCSearcher
	newIRCSearcher = func(context.Context, config.Config, io.Writer) (ircSearcher, func() error, error) {
		h.calls++
		return h.searcher, h.cleanup, h.dialErr
	}
	t.Cleanup(func() { newIRCSearcher = old })
	return h
}

func ircEnvironment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	withGetEnvironment(t, dir)
	for _, key := range []string{
		"BOOXD_IRC_SERVER", "BOOXD_IRC_NICK", "BOOXD_IRC_CHANNEL",
		"BOOXD_IRC_SEARCH_TIMEOUT", "BOOXD_IRC_DOWNLOAD_TIMEOUT", "BOOXD_IRC_DEBUG",
	} {
		t.Setenv(key, "")
	}
	return dir
}

func countingSeams() (resolveFunc, clientFunc, *int, *int) {
	resolves, clients := 0, 0
	resolve := func(context.Context, config.Config, string, *fetch.Client) (string, error) {
		resolves++
		return "", errors.New("unexpected resolver call")
	}
	newClient := func(config.Config, io.Writer, http.CookieJar) *fetch.Client {
		clients++
		return nil
	}
	return resolve, newClient, &resolves, &clients
}

func ircTestBook() sourceirc.Book {
	return sourceirc.Book{Server: "artemis_serv", ID: "16d6770d2ba9", Name: "The Last Hero", Format: "pdf", Size: "9.1 MB"}
}

func TestSearchIRC(t *testing.T) {
	cfg := config.Config{IRCChannel: "#ebooks", IRCSearchTimeout: time.Second}
	for _, tt := range []struct {
		name               string
		books              []sourceirc.Book
		searchErr, dialErr error
		code, searchCalls  int
		out, err           string
	}{
		{
			name: "success", books: []sourceirc.Book{ircTestBook()},
			code: 0, searchCalls: 1, out: ircRow, err: ircNotice,
		},
		{
			name: "no results", code: 0, searchCalls: 1,
			err: ircNotice + "no results\n",
		},
		{
			name: "dial error", dialErr: errors.New("irc: dial failed"),
			code: 1, searchCalls: 0, err: ircNotice + "booxd: irc: dial failed\n",
		},
		{
			name: "search error", searchErr: errors.New("source irc: search \"q\": no offer"),
			code: 1, searchCalls: 1, err: ircNotice + "booxd: source irc: search \"q\": no offer\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleaned := 0
			f := &fakeIRCSearcher{books: tt.books, err: tt.searchErr}
			h := stubIRCSearcher(t, f, func() error { cleaned++; return nil }, tt.dialErr)
			var stdout, stderr bytes.Buffer
			code := searchIRC(cfg, "q", nil, &stdout, &stderr)
			if code != tt.code || stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout.String(), stderr.String(), tt.code, tt.out, tt.err)
			}
			if h.calls != 1 || f.calls != tt.searchCalls || cleaned != 1 {
				t.Fatalf("dial=%d search=%d cleanup=%d; want 1 %d 1", h.calls, f.calls, cleaned, tt.searchCalls)
			}
		})
	}
}

func TestSearchIRCFlags(t *testing.T) {
	for _, tt := range []struct {
		name     string
		flags    []string
		code     int
		out, err string
	}{
		{name: "mirror", flags: []string{"--mirror", "https://x"}, code: 2, err: "booxd: --mirror applies to anna only\n"},
		{name: "dump-html", flags: []string{"--dump-html", "dump.html"}, code: 2, err: "booxd: --dump-html applies to anna only\n"},
		{name: "max-age", flags: []string{"--max-age", "1s"}, code: 2, err: "booxd: --max-age applies to anna only\n"},
		{name: "page", flags: []string{"--page", "2"}, code: 2, err: "booxd: --page applies to anna and zlibrary only\n"},
		{name: "language", flags: []string{"--language", "en"}, code: 2, err: "booxd: --language applies to zlibrary only\n"},
		{name: "format", flags: []string{"--format", "pdf"}, code: 2, err: "booxd: --format applies to zlibrary only\n"},
		{name: "no-cache is a no-op", flags: []string{"--no-cache"}, code: 0, out: ircRow, err: ircNotice},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ircEnvironment(t)
			stubIRCSearcher(t, &fakeIRCSearcher{books: []sourceirc.Book{ircTestBook()}}, func() error { return nil }, nil)
			resolve, newClient, resolves, clients := countingSeams()
			args := append([]string{"search", "--source", "irc"}, tt.flags...)
			args = append(args, "q")
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != tt.code || stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout.String(), stderr.String(), tt.code, tt.out, tt.err)
			}
			if *resolves != 0 || *clients != 0 {
				t.Fatalf("resolver=%d client=%d; want 0 0", *resolves, *clients)
			}
		})
	}
}

func TestSplitServer(t *testing.T) {
	for _, tt := range []struct {
		addr, host string
		port       int
		ok         bool
	}{
		{addr: "irc.example.org:6697", host: "irc.example.org", port: 6697, ok: true},
		{addr: "irc.example.org"},
		{addr: "irc.example.org:chat"},
		{addr: "irc.example.org:0"},
		{addr: "irc.example.org:70000"},
	} {
		t.Run(tt.addr, func(t *testing.T) {
			host, port, err := splitServer(tt.addr)
			if tt.ok {
				if err != nil || host != tt.host || port != tt.port {
					t.Fatalf("splitServer(%q) = %q %d %v; want %q %d nil", tt.addr, host, port, err, tt.host, tt.port)
				}
				return
			}
			if err == nil {
				t.Fatalf("splitServer(%q) = %q %d nil; want an error", tt.addr, host, port)
			}
		})
	}
}

func TestSearchIRCDoesNotTouchHTTP(t *testing.T) {
	ircEnvironment(t)
	stubIRCSearcher(t, &fakeIRCSearcher{books: []sourceirc.Book{ircTestBook()}}, func() error { return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "--source", "irc", "dune"}, &stdout, &stderr, resolve, newClient)
	if code != 0 || stdout.String() != ircRow || stderr.String() != ircNotice {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if *resolves != 0 || *clients != 0 {
		t.Fatalf("resolver=%d client=%d; want 0 0", *resolves, *clients)
	}
}

func ircRef() string { return ircTestBook().Ref().String() }

const ircRequesting = "irc: requesting The Last Hero.pdf from artemis_serv\n"

func TestGetIRC(t *testing.T) {
	dir := ircEnvironment(t)
	wantPath := filepath.Join(dir, "The Last Hero.pdf")
	cleaned := 0
	f := &fakeIRCSearcher{fetch: func(_ context.Context, book sourceirc.Book, dst string) (int64, error) {
		if book.Ref() != ircTestBook().Ref() || book.Name != "The Last Hero" || book.Format != "pdf" || dst != wantPath {
			t.Errorf("Fetch(book=%+v, dst=%q); want ref %q, name %q, format %q, dst %q", book, dst, ircTestBook().Ref().String(), "The Last Hero", "pdf", wantPath)
		}
		if err := os.WriteFile(dst, []byte("book"), 0o644); err != nil {
			return 0, err
		}
		return 4, nil
	}}
	h := stubIRCSearcher(t, f, func() error { cleaned++; return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient)
	if code != 0 || stdout.String() != "saved "+wantPath+"\n" || stderr.String() != ircRequesting {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if h.calls != 1 || f.fetchCalls != 1 || cleaned != 1 {
		t.Fatalf("dial=%d fetch=%d cleanup=%d; want 1 1 1", h.calls, f.fetchCalls, cleaned)
	}
	if *resolves != 0 || *clients != 0 {
		t.Fatalf("resolver=%d client=%d; want 0 0", *resolves, *clients)
	}
	if body, err := os.ReadFile(wantPath); err != nil || string(body) != "book" {
		t.Fatalf("saved body=%q err=%v", body, err)
	}
	if _, err := os.Stat(wantPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover .part: %v", err)
	}
}

func TestGetIRCAlreadyPresent(t *testing.T) {
	dir := ircEnvironment(t)
	wantPath := filepath.Join(dir, "The Last Hero.pdf")
	if err := os.WriteFile(wantPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeIRCSearcher{}
	h := stubIRCSearcher(t, f, func() error { return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient)
	if code != 0 || stdout.String() != "already downloaded: "+wantPath+"\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if h.calls != 0 || f.fetchCalls != 0 || *resolves != 0 || *clients != 0 {
		t.Fatalf("dial=%d fetch=%d resolver=%d client=%d; want all 0", h.calls, f.fetchCalls, *resolves, *clients)
	}
	if body, err := os.ReadFile(wantPath); err != nil || string(body) != "old" {
		t.Fatalf("existing body=%q err=%v", body, err)
	}
}

func TestGetIRCError(t *testing.T) {
	dir := ircEnvironment(t)
	part := filepath.Join(dir, "The Last Hero.pdf.part")
	message := `source irc: fetch "x": wrote 1 of 2 bytes; partial kept at p`
	f := &fakeIRCSearcher{fetch: func(_ context.Context, _ sourceirc.Book, dst string) (int64, error) {
		if err := os.WriteFile(dst+".part", []byte("x"), 0o644); err != nil {
			return 0, err
		}
		return 1, errors.New(message)
	}}
	h := stubIRCSearcher(t, f, func() error { return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient)
	if code != 1 || stdout.Len() != 0 || stderr.String() != ircRequesting+"booxd: "+message+"\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if h.calls != 1 || f.fetchCalls != 1 || *resolves != 0 || *clients != 0 {
		t.Fatalf("dial=%d fetch=%d resolver=%d client=%d", h.calls, f.fetchCalls, *resolves, *clients)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("partial was removed: %v", err)
	}
}

func TestGetIRCCancel(t *testing.T) {
	ircEnvironment(t)
	t.Setenv("BOOXD_IRC_DOWNLOAD_TIMEOUT", "1ms")
	cleaned := 0
	f := &fakeIRCSearcher{fetch: func(ctx context.Context, _ sourceirc.Book, _ string) (int64, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	stubIRCSearcher(t, f, func() error { cleaned++; return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient)
	if code != 1 || stdout.Len() != 0 || stderr.String() != ircRequesting+"booxd: context deadline exceeded\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if f.fetchCalls != 1 || cleaned != 1 || *resolves != 0 || *clients != 0 {
		t.Fatalf("fetch=%d cleanup=%d resolver=%d client=%d", f.fetchCalls, cleaned, *resolves, *clients)
	}
}

func TestGetIRCRestart(t *testing.T) {
	dir := ircEnvironment(t)
	wantPath := filepath.Join(dir, "The Last Hero.pdf")
	part := wantPath + ".part"
	f := &fakeIRCSearcher{}
	f.fetch = func(ctx context.Context, _ sourceirc.Book, dst string) (int64, error) {
		if f.fetchCalls == 1 {
			if err := os.WriteFile(dst+".part", []byte("partial"), 0o644); err != nil {
				return 0, err
			}
			<-ctx.Done()
			return 7, ctx.Err()
		}
		if err := os.Remove(dst + ".part"); err != nil {
			return 0, err
		}
		if err := os.WriteFile(dst, []byte("good"), 0o644); err != nil {
			return 0, err
		}
		return 4, nil
	}
	cleaned := 0
	stubIRCSearcher(t, f, func() error { cleaned++; return nil }, nil)
	resolve, newClient, resolves, clients := countingSeams()

	t.Setenv("BOOXD_IRC_DOWNLOAD_TIMEOUT", "1ms")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient); code != 1 {
		t.Fatalf("first attempt code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("first attempt kept no partial: %v", err)
	}
	if _, err := os.Stat(wantPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first attempt created the destination: %v", err)
	}

	t.Setenv("BOOXD_IRC_DOWNLOAD_TIMEOUT", "30s")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"get", ircRef()}, &stdout, &stderr, resolve, newClient); code != 0 || stdout.String() != "saved "+wantPath+"\n" {
		t.Fatalf("second attempt code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if f.fetchCalls != 2 || cleaned != 2 || *resolves != 0 || *clients != 0 {
		t.Fatalf("fetch=%d cleanup=%d resolver=%d client=%d", f.fetchCalls, cleaned, *resolves, *clients)
	}
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second attempt left a partial: %v", err)
	}
	if body, err := os.ReadFile(wantPath); err != nil || string(body) != "good" {
		t.Fatalf("saved body=%q err=%v", body, err)
	}
}

func TestGetIRCFlags(t *testing.T) {
	dir := ircEnvironment(t)
	for _, tt := range []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "cookies", flags: []string{"--cookies", filepath.Join(dir, "cookies.txt")}, want: "booxd: --cookies applies to anna only\n"},
		{name: "mirror", flags: []string{"--mirror", "https://mirror.test"}, want: "booxd: --mirror applies to anna only\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeIRCSearcher{}
			h := stubIRCSearcher(t, f, func() error { return nil }, nil)
			resolve, newClient, resolves, clients := countingSeams()
			args := append([]string{"get"}, tt.flags...)
			args = append(args, ircRef())
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, resolve, newClient)
			if code != 2 || stdout.Len() != 0 || stderr.String() != tt.want {
				t.Fatalf("code=%d stdout=%q stderr=%q; want 2 %q", code, stdout.String(), stderr.String(), tt.want)
			}
			if h.calls != 0 || f.fetchCalls != 0 || *resolves != 0 || *clients != 0 {
				t.Fatalf("dial=%d fetch=%d resolver=%d client=%d; want all 0", h.calls, f.fetchCalls, *resolves, *clients)
			}
		})
	}
}

func TestGetIRCRejectsBadRef(t *testing.T) {
	ircEnvironment(t)
	for _, ref := range []string{"irc:16d6770d2ba9", `irc:!artemis_serv 16d6770d2ba9 | The Last Hero\n`} {
		t.Run(ref, func(t *testing.T) {
			f := &fakeIRCSearcher{}
			stubIRCSearcher(t, f, func() error { return nil }, nil)
			resolve, newClient, resolves, clients := countingSeams()
			var stdout, stderr bytes.Buffer
			code := run([]string{"get", ref}, &stdout, &stderr, resolve, newClient)
			if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "booxd: source irc:") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if f.fetchCalls != 0 || *resolves != 0 || *clients != 0 {
				t.Fatalf("fetch=%d resolver=%d client=%d; want all 0", f.fetchCalls, *resolves, *clients)
			}
		})
	}
}
