package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostSendsFormBodyAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || string(body) != "email=a%40b&q=x+y" ||
			r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			r.Header.Get("User-Agent") != "custom" || r.Header.Get("X-Test") != "yes" {
			t.Errorf("method %s body %q header %v", r.Method, body, r.Header)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer server.Close()
	header := http.Header{"User-Agent": {"caller"}, "X-Test": {"yes"}}
	form := url.Values{"q": {"x y"}, "email": {"a@b"}}
	resp, err := New(WithUserAgent("custom")).Post(context.Background(), server.URL, form, header)
	if err != nil || resp.StatusCode != http.StatusBadRequest || string(resp.Body) != "bad" {
		t.Fatalf("response %v error %v", resp, err)
	}
	if header.Get("User-Agent") != "caller" || header.Get("Content-Type") != "" {
		t.Fatalf("caller header mutated: %v", header)
	}
}

func TestPostNilHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("User-Agent") != DefaultUserAgent {
			t.Errorf("header %v", r.Header)
		}
	}))
	defer server.Close()
	if _, err := New().Post(context.Background(), server.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPostBypassesCache(t *testing.T) {
	var calls, lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("server"))
	}))
	defer server.Close()
	lookup := func(context.Context, string) (int, []byte, bool) {
		lookups.Add(1)
		return 0, nil, false
	}
	client := New(WithLookup(lookup), WithInterval(0))
	if _, err := client.Post(context.Background(), server.URL, url.Values{"q": {"x"}}, nil); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 0 {
		t.Fatalf("Post consulted the cache %d times", lookups.Load())
	}
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil || string(resp.Body) != "server" || calls.Load() != 2 {
		t.Fatalf("response %v error %v calls %d", resp, err, calls.Load())
	}
}

func TestCacheHitDoesNotServePost(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("server"))
	}))
	defer server.Close()
	lookup := func(context.Context, string) (int, []byte, bool) { return 200, []byte("cached"), true }
	client := New(WithLookup(lookup), WithInterval(0))
	if resp, err := client.Get(context.Background(), server.URL); err != nil || string(resp.Body) != "cached" || calls.Load() != 0 {
		t.Fatalf("get response %v error %v calls %d", resp, err, calls.Load())
	}
	resp, err := client.Post(context.Background(), server.URL, url.Values{"q": {"x"}}, nil)
	if err != nil || string(resp.Body) != "server" || calls.Load() != 1 {
		t.Fatalf("post response %v error %v calls %d", resp, err, calls.Load())
	}
}

func TestPostDoesNotFollowRedirects(t *testing.T) {
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("redirect body"))
	}))
	defer server.Close()
	resp, err := New().Post(context.Background(), server.URL, url.Values{"q": {"x"}}, nil)
	if err != nil || resp.StatusCode != http.StatusFound || string(resp.Body) != "redirect body" || requests.Load() != 0 {
		t.Fatalf("response %v error %v destination requests %d", resp, err, requests.Load())
	}
}

func TestPostJarCookieStaysOnOrigin(t *testing.T) {
	cookies := make(chan string, 2)
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		value := ""
		if cookie, err := r.Cookie("sid"); err == nil {
			value = cookie.Value
		}
		cookies <- value
	})
	a := httptest.NewServer(handler)
	defer a.Close()
	b := httptest.NewServer(handler)
	defer b.Close()
	// The jar scopes by host, not port, so B must be a different host name.
	bURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(a.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "secret"}})
	client := New(WithJar(jar), WithInterval(0))
	for _, target := range []string{a.URL, bURL} {
		if _, err := client.Post(context.Background(), target, url.Values{"q": {"x"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := []string{<-cookies, <-cookies}; !slices.Equal(got, []string{"secret", ""}) {
		t.Fatalf("cookies = %q", got)
	}
}

func TestPostRetriesWithBody(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			bodies := make(chan string, maxAttempts)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies <- string(body)
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(status)
				}
			}))
			defer server.Close()
			clock := &fakeClock{now: time.Unix(1, 0)}
			resp, err := New(WithClock(clock)).Post(context.Background(), server.URL, url.Values{"q": {"x"}}, nil)
			if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != 2 || len(clock.waits) == 0 {
				t.Fatalf("response %v error %v calls %d waits %v", resp, err, calls.Load(), clock.waits)
			}
			if got := []string{<-bodies, <-bodies}; !slices.Equal(got, []string{"q=x", "q=x"}) {
				t.Fatalf("bodies = %q", got)
			}
		})
	}
}

func TestPostSharesHostSpacingWithGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithClock(clock))
	if _, err := client.Get(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Post(context.Background(), server.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(clock.waits, []time.Duration{DefaultInterval}) {
		t.Fatalf("waits = %v", clock.waits)
	}
}
