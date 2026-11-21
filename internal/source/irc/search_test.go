package irc

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
	ircreal "github.com/curedbylethe/booxd/internal/irc"
)

func TestSearchHappyPath(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := zipFixture(t, lineA+"\n"+lineB+"\n")
	f.send(dcc.Offer{From: "search", Name: "results.zip", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)

	got, err := s.Search(context.Background(), "jane austen")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []Book{bookA, bookB}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search books = %+v, want %+v", got, want)
	}
	texts, targets := f.waitSays(t, 1)
	if !reflect.DeepEqual(texts, []string{"@search jane austen"}) {
		t.Fatalf("Say texts = %q, want %q", texts, "@search jane austen")
	}
	if !reflect.DeepEqual(targets, []string{"#ebooks"}) {
		t.Fatalf("Say targets = %q, want %q", targets, "#ebooks")
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestSearchFallback(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := zipFixture(t, lineA+"\n")
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)

	// The first bot's 50ms window expires before the fallback offers; each
	// accepted name gets its own ZipWait (Q-03).
	go func() {
		timer := time.NewTimer(85 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		f.send(dcc.Offer{From: "searchook", Name: "results.zip", Size: int64(len(data))})
	}()

	got, err := s.Search(context.Background(), "fallback")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !reflect.DeepEqual(got, []Book{bookA}) {
		t.Fatalf("Search books = %+v, want %+v", got, []Book{bookA})
	}
}

func TestSearchTimeout(t *testing.T) {
	f := newFakeClient("#ebooks")
	s := testSearcher(f)

	_, err := s.Search(context.Background(), "nothing")
	if err == nil {
		t.Fatal("Search error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("error %q does not name the query", err)
	}
	if !strings.Contains(err.Error(), `"search"`) {
		t.Fatalf("error %q does not name the bot", err)
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestSearchWrongSender(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := zipFixture(t, lineB+"\n")
	f.send(dcc.Offer{From: "someone", Name: "junk.zip", Size: int64(len(data))})
	f.send(dcc.Offer{From: "search", Name: "results.zip", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)

	got, err := s.Search(context.Background(), "wrong sender")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !reflect.DeepEqual(got, []Book{bookB}) {
		t.Fatalf("Search books = %+v, want the offer from search %+v", got, []Book{bookB})
	}
}

func TestSearchSayError(t *testing.T) {
	f := newFakeClient("#ebooks")
	f.setSayErr(ircreal.ErrSearchPending)
	s := testSearcher(f)

	_, err := s.Search(context.Background(), "pending")
	if !errors.Is(err, ircreal.ErrSearchPending) {
		t.Fatalf("Search error = %v, want ErrSearchPending", err)
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Fatalf("error %q does not name the query", err)
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestSearchCancel(t *testing.T) {
	f := newFakeClient("#ebooks")
	s := testSearcher(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, err := s.Search(ctx, "cancel")
		done <- result{err}
	}()

	f.waitSays(t, 1)
	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("Search error = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after cancel")
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestSearchCorruptArchive(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := []byte("not a zip")
	f.send(dcc.Offer{From: "search", Name: "bad.zip", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)

	if _, err := s.Search(context.Background(), "corrupt"); err == nil {
		t.Fatal("Search error = nil, want corrupt archive error")
	}
	if n := f.releaseCount(); n != 1 {
		t.Fatalf("ReleaseSearch calls = %d, want 1", n)
	}
}

func TestSearchEarlyFallbackOffer(t *testing.T) {
	f := newFakeClient("#ebooks")
	data := zipFixture(t, lineA+"\n"+lineB+"\n")
	f.send(dcc.Offer{From: "searchook", Name: "results.zip", Size: int64(len(data))})
	s := testSearcher(f)
	s.openOffer = pipeOpen(t, data)

	// The fallback bot answers during the primary bot's window; the offer
	// must not be discarded.
	got, err := s.Search(context.Background(), "early")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []Book{bookA, bookB}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search books = %+v, want %+v", got, want)
	}
}

func TestSearchStalledStreamCancel(t *testing.T) {
	f := newFakeClient("#ebooks")
	s := testSearcher(f)
	opened := make(chan struct{})
	s.openOffer = func(context.Context, dcc.Offer) (io.ReadCloser, error) {
		reader, _ := net.Pipe()
		close(opened)
		return reader, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := s.Search(ctx, "stall")
		done <- err
	}()

	f.waitSays(t, 1)
	f.send(dcc.Offer{From: "search", Name: "results.zip", Size: 1})
	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("offer never opened")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Search error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after cancellation")
	}
}
