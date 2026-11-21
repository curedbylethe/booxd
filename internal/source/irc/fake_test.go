package irc

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
	ircreal "github.com/curedbylethe/booxd/internal/irc"
)

// The production package stays independent of internal/irc; only this test
// imports it, under an alias, to assert the interface still fits (G-16).
var _ Client = (*ircreal.Client)(nil)

// fakeClient is a scripted Client for Search (and later Fetch) tests. It never
// touches the network: tests post offers with send and observe Say with
// waitSays.
type fakeClient struct {
	mu       sync.Mutex
	channel  string
	says     []string // exact text of each Say
	targets  []string // target of each Say
	said     chan struct{}
	sayErr   error
	releases int
	offers   chan dcc.Offer
}

func newFakeClient(channel string) *fakeClient {
	return &fakeClient{
		channel: channel,
		said:    make(chan struct{}, 16),
		offers:  make(chan dcc.Offer, 16),
	}
}

func (f *fakeClient) Channel() string { return f.channel }

func (f *fakeClient) Say(_ context.Context, target, text string) error {
	f.mu.Lock()
	f.says = append(f.says, text)
	f.targets = append(f.targets, target)
	err := f.sayErr
	f.mu.Unlock()
	if err == nil {
		f.said <- struct{}{}
	}
	return err
}

func (f *fakeClient) DCC() <-chan dcc.Offer { return f.offers }

func (f *fakeClient) ReleaseSearch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
}

// send posts an offer to the buffered channel; it never blocks in a test.
func (f *fakeClient) send(offer dcc.Offer) { f.offers <- offer }

// setSayErr injects a Say failure for the next searches.
func (f *fakeClient) setSayErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sayErr = err
}

// waitSays waits for n recorded sends, then returns their texts and targets.
func (f *fakeClient) waitSays(t *testing.T, n int) ([]string, []string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-f.said:
		case <-deadline:
			f.mu.Lock()
			got := append([]string(nil), f.says...)
			f.mu.Unlock()
			t.Fatalf("timed out waiting for %d Say calls, got %q", n, got)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.says...), append([]string(nil), f.targets...)
}

func (f *fakeClient) releaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.releases
}

// testSearcher shortens ZipWait so tests never wait the production 120s.
func testSearcher(f *fakeClient) *Searcher {
	s := New(f)
	s.ZipWait = 50 * time.Millisecond
	return s
}

// pipeOpen serves data over net.Pipe through the openOffer seam. Offer.Size in
// the test must match len(data) or readOffer rejects the byte count.
func pipeOpen(t *testing.T, data []byte) func(context.Context, dcc.Offer) (io.ReadCloser, error) {
	t.Helper()
	return func(context.Context, dcc.Offer) (io.ReadCloser, error) {
		reader, writer := net.Pipe()
		go func() {
			defer writer.Close()
			_, _ = writer.Write(data)
		}()
		return reader, nil
	}
}
