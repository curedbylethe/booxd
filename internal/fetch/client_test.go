package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetStatus(t *testing.T) {
	for _, status := range []int{200, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.UserAgent() != userAgent {
					t.Errorf("method = %q, User-Agent = %q", r.Method, r.UserAgent())
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("exact body\x00\n"))
			}))
			t.Cleanup(server.Close)
			resp, err := New().Get(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != status || string(resp.Body) != "exact body\x00\n" {
				t.Fatalf("response = %#v, want status %d and exact body", resp, status)
			}
		})
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
	client := New()
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
	client := New()
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
}
