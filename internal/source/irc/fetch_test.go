package irc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

func TestFetchHappyPath(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("book body bytes")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dst := filepath.Join(t.TempDir(), "book.epub")

	written, err := s.Fetch(context.Background(), bookA, dst)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if written != int64(len(data)) {
		t.Fatalf("written = %d, want %d", written, len(data))
	}
	fetchAssertFile(t, dst, string(data))
	fetchAssertMissing(t, dst+".part")

	texts, targets := f.waitSays(t, 1)
	wantLine := "!artemis_serv 16d6770d2ba9 | Name.epub"
	if len(texts) != 1 || texts[0] != bookA.requestLine() || texts[0] != wantLine {
		t.Fatalf("Say texts = %q, want [%q] (requestLine)", texts, wantLine)
	}
	if texts[0] != strings.TrimSpace(strings.Split(bookA.Raw, "::INFO::")[0]) {
		t.Fatalf("request line %q is not Raw without the ::INFO:: tail", texts[0])
	}
	if len(targets) != 1 || targets[0] != "#ebooks" {
		t.Fatalf("Say targets = %q, want [#ebooks]", targets)
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestFetchWrongSenderIgnored(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("from the right server")
	f.send(dcc.Offer{From: "impostor", Name: "bad.epub", Size: 7})
	f.send(dcc.Offer{From: "ARTEMIS_SERV", Name: "Name.epub", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dst := filepath.Join(t.TempDir(), "book.epub")

	written, err := s.Fetch(context.Background(), bookA, dst)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if written != int64(len(data)) {
		t.Fatalf("written = %d, want %d", written, len(data))
	}
	fetchAssertFile(t, dst, string(data))
}

func TestFetchShortBodyKeepsPartial(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("short body")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data) + 5)})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dst := filepath.Join(t.TempDir(), "book.epub")

	written, err := s.Fetch(context.Background(), bookA, dst)
	if err == nil {
		t.Fatal("Fetch error = nil, want byte-count mismatch")
	}
	if written != int64(len(data)) {
		t.Fatalf("written = %d, want %d", written, len(data))
	}
	for _, part := range []string{"10", "5", "wrote"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q does not mention %q", err, part)
		}
	}
	fetchAssertFile(t, dst+".part", string(data))
	fetchAssertMissing(t, dst)
}

func TestFetchStalePartRestarted(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("fresh")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dir := t.TempDir()
	dst := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(dst+".part", []byte("stale junk that must not survive"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Fetch(context.Background(), bookA, dst); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	fetchAssertFile(t, dst, string(data))
	fetchAssertMissing(t, dst+".part")
}

func TestFetchReplacesExistingFinal(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("new contents")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dst := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(dst, []byte("old contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Fetch(context.Background(), bookA, dst); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	fetchAssertFile(t, dst, string(data))
	fetchAssertMissing(t, dst+".part")
}

func TestFetchCancelKeepsPartial(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("partial then cancel")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data) + 100)})
	s := testSearcher(f)
	opener, release := blockedPipeOpen(data)
	s.openOffer = opener
	defer release()
	dst := filepath.Join(t.TempDir(), "book.epub")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-f.said // cancel once Say has landed and Fetch is streaming
		cancel()
	}()

	_, err := s.Fetch(ctx, bookA, dst)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dst + ".part"); statErr != nil {
		t.Fatalf("stat .part: %v, want kept file", statErr)
	}
	fetchAssertMissing(t, dst)
}

func TestFetchNoOfferTimeout(t *testing.T) {
	f := newFakeClient("#ebooks")
	s := testSearcher(f)
	s.OfferWait = 50 * time.Millisecond
	dst := filepath.Join(t.TempDir(), "book.epub")

	_, err := s.Fetch(context.Background(), bookA, dst)
	if err == nil {
		t.Fatal("Fetch error = nil, want timeout")
	}
	for _, part := range []string{bookA.ID, bookA.Server, "no offer"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q does not mention %q", err, part)
		}
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
	fetchAssertMissing(t, dst)
}

func TestFetchZeroByteOffer(t *testing.T) {
	f := newFakeClient("#ebooks")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: 0})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, nil)
	dst := filepath.Join(t.TempDir(), "book.epub")

	written, err := s.Fetch(context.Background(), bookA, dst)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}
	fetchAssertFile(t, dst, "")
	fetchAssertMissing(t, dst+".part")
}

func TestFetchValidationAndSayError(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		bad := []Book{{}, bookA, bookA}
		bad[1].Raw = ""
		bad[2].Server = ""
		paths := []string{"", "x", "x"}
		for i := range bad {
			f := newFakeClient("#ebooks")
			s := testSearcher(f)
			if _, err := s.Fetch(context.Background(), bad[i], paths[i]); err == nil || !strings.Contains(err.Error(), "source irc: fetch") {
				t.Fatalf("case %d: error = %v, want source irc: fetch prefix", i, err)
			}
			if n := f.releaseCount(); n != 1 {
				t.Fatalf("case %d: ReleaseSearch calls = %d, want 1", i, n)
			}
		}
	})
	t.Run("say error", func(t *testing.T) {
		f := newFakeClient("#ebooks")
		f.setSayErr(errors.New("send failed"))
		s := testSearcher(f)
		if _, err := s.Fetch(context.Background(), bookA, filepath.Join(t.TempDir(), "book.epub")); err == nil || !strings.Contains(err.Error(), "send failed") {
			t.Fatalf("error = %v, want wrapped send failure", err)
		}
	})
}

// blockedPipeOpen serves data then holds the pipe open until release, so a
// Fetch whose context is cancelled must be unblocked by AfterFunc closing the
// reader end, not by EOF. No real dial, sleep, or background daemon.
func blockedPipeOpen(data []byte) (func(context.Context, dcc.Offer) (io.ReadCloser, error), func()) {
	release := make(chan struct{})
	opener := func(context.Context, dcc.Offer) (io.ReadCloser, error) {
		reader, writer := net.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			_, _ = writer.Write(data)
			<-release
		}()
		return reader, nil
	}
	return opener, func() { close(release) }
}

func fetchAssertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s = %q, error = %v; want %q", path, got, err, want)
	}
}

func fetchAssertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat %s error = %v, want not exist", path, err)
	}
}

func TestFetchRenameFailureKeepsDestination(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("book body bytes")
	f.send(dcc.Offer{From: "artemis_serv", Name: "Name.epub", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)
	dst := filepath.Join(t.TempDir(), "book.epub")
	// The destination is an existing directory: the replacement must fail
	// and leave it — and its contents — untouched.
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	inside := filepath.Join(dst, "keep.txt")
	if err := os.WriteFile(inside, []byte("kept"), 0o644); err != nil {
		t.Fatalf("write inside: %v", err)
	}

	_, err := s.Fetch(context.Background(), bookA, dst)
	if err == nil {
		t.Fatal("Fetch: expected a rename failure")
	}
	fetchAssertFile(t, inside, "kept")
	if _, serr := os.Stat(dst + ".old"); serr == nil {
		t.Fatal("the existing destination was moved aside")
	}
}
