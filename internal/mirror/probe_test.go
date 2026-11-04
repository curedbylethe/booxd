package mirror

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestSeeds(t *testing.T) {
	seeds := Seeds()
	suffixes := []string{".is", ".gl", ".gd", ".pk"}
	if len(seeds) != len(suffixes) {
		t.Fatalf("seed count = %d, want %d", len(seeds), len(suffixes))
	}
	for i, seed := range seeds {
		u, err := url.Parse(seed)
		if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Host, suffixes[i]) || u.Path != "" || strings.TrimSpace(seed) != seed {
			t.Fatalf("seed %d = %q, want HTTPS base with suffix %q", i, seed, suffixes[i])
		}
	}
	first := seeds[0]
	seeds[0] = "changed"
	if got := Seeds()[0]; got != first {
		t.Fatalf("Seeds reused mutable slice: got %q, want %q", got, first)
	}
}

// probeClock advances retry and interval waits without delaying the test suite.
type probeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *probeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *probeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
func (c *probeClock) Wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d >= time.Minute {
		return context.DeadlineExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

type probeTransportFunc func(*http.Request) (*http.Response, error)

func (f probeTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProbe(t *testing.T) {
	if probeTimeout != 5*time.Second {
		t.Fatalf("probe timeout = %v, want 5s", probeTimeout)
	}
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		wait      bool
		closed    bool
		malformed bool
		cancelled bool
		transport error
		want      outcome
		wantCalls int32
	}{
		{name: "pass", status: 200, body: `<html><title>Search results for "test"</title></html>`, want: pass, wantCalls: 1},
		{name: "marker after prefix", status: 200, body: "prefix\n<title>Search results", want: pass, wantCalls: 1},
		{name: "no marker", status: 200, body: "<title>Anna's Archive</title>", want: fail, wantCalls: 1},
		{name: "empty body", status: 200, want: fail, wantCalls: 1},
		{name: "403 with marker", status: 403, body: passMarker, want: dead, wantCalls: 1},
		{name: "404", status: 404, body: "missing", want: dead, wantCalls: 1},
		{name: "timeout", wait: true, want: dead, wantCalls: 1},
		{name: "429", status: 429, body: "limited", want: fail, wantCalls: 1},
		{name: "500", status: 500, body: "failed", want: fail, wantCalls: 3},
		{name: "redirect", status: 302, body: passMarker, want: fail, wantCalls: 1},
		{name: "transport error", closed: true, want: fail},
		{name: "DNS error", transport: &net.DNSError{Err: "no such host", Name: "booxd.invalid", IsNotFound: true}, want: fail},
		{name: "transport timeout", transport: &net.DNSError{Err: "timeout", Name: "booxd.invalid", IsTimeout: true}, want: dead},
		{name: "malformed base", malformed: true, want: fail},
		{name: "cancelled", cancelled: true, want: fail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.RequestURI() != probePath {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.RequestURI(), probePath)
				}
				if tt.wait {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)
			base := server.URL + "/"
			if tt.closed {
				server.Close()
			}
			if tt.malformed {
				base = "://bad"
			}
			if tt.transport != nil {
				original := http.DefaultTransport
				http.DefaultTransport = probeTransportFunc(func(*http.Request) (*http.Response, error) {
					return nil, tt.transport
				})
				t.Cleanup(func() { http.DefaultTransport = original })
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			timeout := time.Second
			if tt.wait || tt.status == 429 {
				timeout = 20 * time.Millisecond
			}
			client := fetch.New(fetch.WithClock(&probeClock{now: time.Unix(1, 0)}))
			if tt.status == 500 {
				timeout = 2 * time.Second
			}
			if got := probe(ctx, client, base, timeout); got != tt.want {
				t.Fatalf("outcome = %d, want %d", got, tt.want)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Fatalf("requests = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestProbeChallengeIsDead(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Server", "ddos-guard")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	if got := probe(context.Background(), fetch.New(), server.URL, time.Second); got != dead || calls.Load() != 1 {
		t.Fatalf("outcome = %v, calls = %d", got, calls.Load())
	}
}
