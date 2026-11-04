package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	return nil
}

func TestIntervalSpacing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		calls    int
		want     []time.Duration
	}{
		{"same host", DefaultInterval, 2, []time.Duration{DefaultInterval}},
		{"first request", DefaultInterval, 1, nil},
		{"disabled", 0, 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer server.Close()
			clock := &fakeClock{now: time.Unix(1, 0)}
			client := New(WithClock(clock), WithInterval(tc.interval))
			for range tc.calls {
				if _, err := client.Get(context.Background(), server.URL); err != nil {
					t.Fatal(err)
				}
			}
			if int(calls.Load()) != tc.calls || !slices.Equal(clock.waits, tc.want) {
				t.Fatalf("calls = %d, waits = %v; want %d, %v", calls.Load(), clock.waits, tc.calls, tc.want)
			}
		})
	}
}

func TestSystemClockWaitHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := (systemClock{}).Wait(ctx, time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v", err)
	}
}

func TestGetStatus(t *testing.T) {
	for _, status := range []int{200, 403, 404, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.UserAgent() != DefaultUserAgent {
					t.Errorf("method = %q, User-Agent = %q", r.Method, r.UserAgent())
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("exact body\x00\n"))
			}))
			t.Cleanup(server.Close)
			resp, err := New(WithClock(&fakeClock{now: time.Unix(1, 0)})).Get(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != status || string(resp.Body) != "exact body\x00\n" {
				t.Fatalf("response = %#v, want status %d and exact body", resp, status)
			}
		})
	}
}

func TestGetUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != "custom/1" {
			t.Errorf("User-Agent = %q, want custom/1", got)
		}
	}))
	t.Cleanup(server.Close)
	if _, err := New(WithUserAgent("custom/1")).Get(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if got := New(WithUserAgent("")).userAgent; got != DefaultUserAgent {
		t.Fatalf("empty User-Agent option = %q, want default", got)
	}
}

func TestGetUsesProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "unresolvable.invalid" {
			t.Errorf("proxy request host = %q", r.URL.Host)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := New(WithProxy(proxyURL)).Get(context.Background(), "http://unresolvable.invalid/path")
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "proxied" {
		t.Fatalf("body = %q, want proxied", resp.Body)
	}
}

func TestGetTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	t.Cleanup(server.Close)
	resp, err := New(WithTimeout(10*time.Millisecond)).Get(context.Background(), server.URL)
	if resp != nil || err == nil {
		t.Fatalf("response = %#v, error = %v; want timeout", resp, err)
	}
	// The probe classifies timeouts with errors.Is, so the wrapper must keep it true.
	host := server.Listener.Addr().String()
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "fetch: "+host+" timed out connecting or waiting for a response after ") {
		t.Fatalf("error = %v; want named timeout wrapping context.DeadlineExceeded", err)
	}
	if got := New(WithTimeout(0)).http.Timeout; got != 0 {
		t.Fatalf("zero timeout = %s, want disabled", got)
	}
	if got := New(WithTimeout(10*time.Second), WithTimeout(-1)).http.Timeout; got != DefaultTimeout {
		t.Fatalf("negative timeout = %s, want %s", got, DefaultTimeout)
	}
}

func TestWithTimeoutZeroBoundsResponseHeaders(t *testing.T) {
	proxy, err := url.Parse("http://proxy.test:8080")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse("http://target.test/file")
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range [][]Option{
		{WithTimeout(0), WithProxy(proxy)},
		{WithProxy(proxy), WithTimeout(0)},
	} {
		client := New(options...)
		transport, ok := client.http.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("transport type = %T, want *http.Transport", client.http.Transport)
		}
		if client.http.Timeout != 0 {
			t.Fatalf("client timeout = %s, want disabled", client.http.Timeout)
		}
		if transport.ResponseHeaderTimeout != DefaultTimeout {
			t.Fatalf("response header timeout = %s, want %s", transport.ResponseHeaderTimeout, DefaultTimeout)
		}
		if proxyURL, err := transport.Proxy(&http.Request{URL: target}); err != nil || proxyURL == nil || proxyURL.String() != proxy.String() {
			t.Fatalf("proxy = %v, error = %v; want %s", proxyURL, err, proxy)
		}
		if client.http.Transport == http.DefaultTransport {
			t.Fatal("client reused the default transport instead of cloning it")
		}
	}
}

func TestWithJarNilIsNoop(t *testing.T) {
	if got := New(WithJar(nil)).http.Jar; got != nil {
		t.Fatalf("nil jar = %v, want nil", got)
	}
}

func TestNewDoesNotMutateDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	proxyURL, err := url.Parse("http://proxy.invalid:3128")
	if err != nil {
		t.Fatal(err)
	}
	_ = New(WithProxy(proxyURL))
	if http.DefaultTransport != original {
		t.Fatal("New mutated http.DefaultTransport")
	}
}

func TestGetTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	resp, err := New().Get(context.Background(), server.URL)
	var urlErr *url.Error
	if resp != nil || !errors.As(err, &urlErr) {
		t.Fatalf("response = %#v, error = %v; want nil response and *url.Error", resp, err)
	}
}

func TestGetDoesNotFollowRedirects(t *testing.T) {
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(destination.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("redirect body"))
	}))
	t.Cleanup(server.Close)
	resp, err := New().Get(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound || string(resp.Body) != "redirect body" || requests.Load() != 0 {
		t.Fatalf("response = %#v, destination requests = %d", resp, requests.Load())
	}
}

func TestGetMalformedURL(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	resp, err := New().Get(context.Background(), "://")
	if resp != nil || err == nil || requests.Load() != 0 {
		t.Fatalf("response = %#v, error = %v, requests = %d", resp, err, requests.Load())
	}
}

func TestGetDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp, err := New().Get(ctx, server.URL)
	if resp != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("response = %#v, error = %v; want deadline exceeded", resp, err)
	}
}

func TestGetSerializesHostThroughBodyRead(t *testing.T) {
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
		_, _ = w.Write([]byte("done"))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock))
	results := make(chan error, 2)
	request := func() {
		resp, err := client.Get(ctx, server.URL)
		if err == nil && (resp.StatusCode != http.StatusOK || string(resp.Body) != "done") {
			err = fmt.Errorf("unexpected response: %#v", resp)
		}
		results <- err
	}
	go request()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first request did not start")
	}
	go request()
	select {
	case <-started:
		t.Fatal("second request started before first body finished")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("requests did not complete")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxInFlight != 1 {
		t.Fatalf("maximum in-flight requests = %d, want 1", maxInFlight)
	}
	if !slices.Equal(clock.waits, []time.Duration{DefaultInterval}) {
		t.Fatalf("waits = %v", clock.waits)
	}
}

func TestGetDifferentHosts(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	a := httptest.NewServer(handler)
	b := httptest.NewServer(handler)
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	t.Cleanup(unblock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock))
	results := make(chan error, 2)
	for _, rawURL := range []string{a.URL, b.URL} {
		go func() {
			resp, err := client.Get(ctx, rawURL)
			if err == nil && resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("status = %d, want 200", resp.StatusCode)
			}
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("different hosts did not start concurrently")
		}
	}
	unblock()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("requests did not complete")
		}
	}
	if len(clock.waits) != 0 {
		t.Fatalf("waits = %v", clock.waits)
	}
}

func TestGet429Backoff(t *testing.T) {
	for _, tt := range []struct {
		name, header string
		success      bool
		waits        []time.Duration
		wantCalls    int32
	}{
		{"header", "2", true, []time.Duration{2 * time.Second}, 2},
		{"exhausted", "", false, []time.Duration{time.Minute, 2 * time.Minute}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := calls.Add(1)
				if tt.success && n > 1 {
					return
				}
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(429)
			}))
			defer server.Close()
			clock := &fakeClock{now: time.Unix(1, 0)}
			resp, err := New(WithClock(clock)).Get(context.Background(), server.URL)
			wantStatus := 429
			if tt.success {
				wantStatus = 200
			}
			if err != nil || resp.StatusCode != wantStatus || calls.Load() != tt.wantCalls || !slices.Equal(clock.waits, tt.waits) {
				t.Fatalf("response %v error %v calls %d waits %v", resp, err, calls.Load(), clock.waits)
			}
		})
	}
}

func TestGet429InterruptedReturnsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	resp, err := New().Get(ctx, server.URL)
	if err != nil || resp == nil || resp.StatusCode != 429 {
		t.Fatalf("response %v error %v", resp, err)
	}
}

func TestGetChallengeMarksUnhealthy(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Server", "ddos-guard")
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock))
	for range 2 {
		_, err := client.Get(context.Background(), server.URL)
		if !errors.Is(err, ErrHostUnhealthy) || !strings.Contains(err.Error(), strings.TrimPrefix(server.URL, "http://")) {
			t.Fatalf("error %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
	if err := clock.Wait(context.Background(), 61*time.Minute); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || resp.StatusCode != 200 || calls.Load() != 2 {
		t.Fatalf("response %v error %v calls %d", resp, err, calls.Load())
	}
}

func TestGet5xxRetry(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) < 3 || !success {
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			clock := &fakeClock{now: time.Unix(1, 0)}
			resp, err := New(WithClock(clock), WithInterval(0)).Get(context.Background(), server.URL)
			if calls.Load() != 3 || len(clock.waits) != 2 || clock.waits[0] < 125*time.Millisecond || clock.waits[0] >= 250*time.Millisecond || clock.waits[1] < 250*time.Millisecond || clock.waits[1] >= 500*time.Millisecond {
				t.Fatalf("calls %d waits %v", calls.Load(), clock.waits)
			}
			if success {
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("response %v error %v", resp, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), strings.TrimPrefix(server.URL, "http://")) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestGetVerbose(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprint(verbose), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(429)
				}
			}))
			defer server.Close()
			var log bytes.Buffer
			var writer io.Writer
			if verbose {
				writer = &log
			}
			clock := &fakeClock{now: time.Unix(1, 0)}
			_, err := New(WithClock(clock), WithVerbose(writer)).Get(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if verbose {
				for _, part := range []string{"host=" + strings.TrimPrefix(server.URL, "http://"), "status=- wait=0s", "status=429 wait=1m0s"} {
					if !strings.Contains(log.String(), part) {
						t.Fatalf("missing %q in %q", part, log.String())
					}
				}
			} else if log.Len() != 0 {
				t.Fatalf("unexpected log %q", log.String())
			}
		})
	}
}

type observingClock struct {
	*fakeClock
	log      *bytes.Buffer
	snapshot string
}

func (c *observingClock) Wait(ctx context.Context, d time.Duration) error {
	c.snapshot = c.log.String()
	return context.Canceled
}

func TestGetVerboseLogsBeforeBackoff(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			var log bytes.Buffer
			clock := &observingClock{fakeClock: &fakeClock{now: time.Unix(1, 0)}, log: &log}
			_, _ = New(WithClock(clock), WithVerbose(&log)).Get(context.Background(), server.URL)
			for _, part := range []string{"host=" + strings.TrimPrefix(server.URL, "http://"), fmt.Sprintf("status=%d waiting=", status)} {
				if !strings.Contains(clock.snapshot, part) {
					t.Fatalf("missing %q before wait in %q", part, clock.snapshot)
				}
			}
		})
	}
}

func TestGet429InterruptedIntervalReturnsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "0"); w.WriteHeader(429) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp, err := New().Get(ctx, server.URL)
	if err != nil || resp == nil || resp.StatusCode != 429 {
		t.Fatalf("response %v error %v", resp, err)
	}
}

func TestGetCacheHitSkipsNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock), WithLookup(func(context.Context, string) (int, []byte, bool) {
		return 200, []byte("cached"), true
	}))
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != "cached" || resp.Header != nil || calls.Load() != 0 || len(clock.waits) != 0 {
		t.Fatalf("response %v error %v calls %d waits %v", resp, err, calls.Load(), clock.waits)
	}
}

func TestGetCacheMissFetches(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("network"))
	}))
	defer server.Close()
	client := New(WithLookup(func(context.Context, string) (int, []byte, bool) { return 0, nil, false }))
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != "network" || calls.Load() != 1 {
		t.Fatalf("response %v error %v calls %d", resp, err, calls.Load())
	}
}

func TestGetCacheKeyIsTheRawURL(t *testing.T) {
	const rawURL = "https://example.com/path?query=a%20b"
	var key string
	client := New(WithLookup(func(_ context.Context, raw string) (int, []byte, bool) {
		key = raw
		return 200, nil, true
	}))
	if _, err := client.Get(context.Background(), rawURL); err != nil || key != rawURL {
		t.Fatalf("key %q error %v", key, err)
	}
}

func TestGetMalformedURLSkipsLookup(t *testing.T) {
	called := false
	client := New(WithLookup(func(context.Context, string) (int, []byte, bool) {
		called = true
		return 200, nil, true
	}))
	if _, err := client.Get(context.Background(), "://"); err == nil || called {
		t.Fatalf("error %v lookup called %t", err, called)
	}
}

func TestOpenStreamsBodyAfterReturn(t *testing.T) {
	continueWrite := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-continueWrite
		_, _ = io.WriteString(w, "streamed")
	}))
	defer server.Close()
	stream, err := New(WithInterval(0)).Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	close(continueWrite)
	body, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil || string(body) != "streamed" {
		t.Fatalf("body = %q, error = %v", body, err)
	}
}

func TestOpenHoldsHostSlotUntilClose(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := New(WithClock(&fakeClock{now: time.Unix(1, 0)}), WithInterval(0))
	first, err := client.Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan error, 1)
	go func() {
		second, err := client.Open(context.Background(), server.URL, nil)
		if err == nil {
			_ = second.Body.Close()
		}
		secondResult <- err
	}()
	select {
	case err := <-secondResult:
		t.Fatalf("second Open returned before first body closed: %v", err)
	default:
	}
	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Open did not start after first body closed")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestOpenSendsHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("Range = %q", got)
		}
		if got := r.UserAgent(); got != "booxd/0.1" {
			t.Errorf("User-Agent = %q", got)
		}
	}))
	defer server.Close()
	stream, err := New(WithInterval(0)).Open(context.Background(), server.URL, http.Header{"Range": []string{"bytes=5-"}, "User-Agent": []string{"ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Body.Close()
}

func TestOpenBypassesLookup(t *testing.T) {
	var lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "network") }))
	defer server.Close()
	client := New(WithInterval(0), WithLookup(func(context.Context, string) (int, []byte, bool) {
		lookups.Add(1)
		return http.StatusOK, []byte("cached"), true
	}))
	stream, err := client.Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil || string(body) != "network" || lookups.Load() != 0 {
		t.Fatalf("body = %q, error = %v, lookups = %d", body, err, lookups.Load())
	}
}

func TestOpenRetries429AndKeepsBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "limited")
			return
		}
		_, _ = io.WriteString(w, "ready")
	}))
	defer server.Close()
	stream, err := New(WithClock(&fakeClock{now: time.Unix(1, 0)}), WithInterval(0)).Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil || string(body) != "ready" || calls.Load() != 2 {
		t.Fatalf("body = %q, error = %v, calls = %d", body, err, calls.Load())
	}
}

func TestOpenChallengeClosesBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "challenge")
	}))
	defer server.Close()
	_, err := New(WithInterval(0)).Open(context.Background(), server.URL, nil)
	if !errors.Is(err, ErrHostUnhealthy) || calls.Load() != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestOpenPlainForbiddenReleasesHostSlot(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "forbidden")
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := New(WithInterval(0), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	first, err := client.Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstBody, err := io.ReadAll(first.Body)
	_ = first.Body.Close()
	if err != nil || string(firstBody) != "forbidden" {
		t.Fatalf("first body = %q, error = %v", firstBody, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := client.Open(ctx, server.URL, nil)
	if err != nil {
		t.Fatalf("second Open failed after plain 403: %v", err)
	}
	body, err := io.ReadAll(second.Body)
	_ = second.Body.Close()
	if err != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Fatalf("second body = %q, error = %v, calls = %d", body, err, calls.Load())
	}
}

func TestOpenPassesRedirectThrough(t *testing.T) {
	var target atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			target.Add(1)
		}
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	stream, err := New(WithInterval(0)).Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Body.Close()
	if stream.StatusCode != http.StatusFound || stream.Header.Get("Location") != "/target" || target.Load() != 0 {
		t.Fatalf("status = %d, location = %q, target requests = %d", stream.StatusCode, stream.Header.Get("Location"), target.Load())
	}
}

func TestOpenSendsJarCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "test" {
			t.Errorf("sid cookie = %v, error = %v", cookie, err)
		}
	}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "test"}})
	stream, err := New(WithJar(jar), WithInterval(0)).Open(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Body.Close()
}

func TestGetCacheHitOnUnhealthyHost(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Server", "ddos-guard")
		w.WriteHeader(403)
	}))
	defer server.Close()
	hit := false
	client := New(WithLookup(func(context.Context, string) (int, []byte, bool) { return 200, []byte("cached"), hit }))
	if _, err := client.Get(context.Background(), server.URL); !errors.Is(err, ErrHostUnhealthy) {
		t.Fatalf("error %v", err)
	}
	hit = true
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || string(resp.Body) != "cached" || calls.Load() != 1 {
		t.Fatalf("response %v error %v calls %d", resp, err, calls.Load())
	}
}

func TestGet429DeadlineSurvivesCalls(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 3 {
			w.WriteHeader(429)
		}
	}))
	defer server.Close()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock))
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || resp.StatusCode != 429 {
		t.Fatalf("response %v error %v", resp, err)
	}
	resp, err = client.Get(context.Background(), server.URL)
	if err != nil || resp.StatusCode != 200 || calls.Load() != 4 || !slices.Equal(clock.waits, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}) {
		t.Fatalf("response %v error %v calls %d waits %v", resp, err, calls.Load(), clock.waits)
	}
}

func TestGet429InterruptedDeadlineSurvivesCalls(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
	}))
	defer server.Close()
	client := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp, err := client.Get(ctx, server.URL)
	if err != nil || resp == nil || resp.StatusCode != 429 {
		t.Fatalf("response %v error %v", resp, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	_, err = client.Get(ctx2, server.URL)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("error %v calls %d", err, calls.Load())
	}
}
