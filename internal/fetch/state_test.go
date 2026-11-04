package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetryStateRestartAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fetch.json")
	clock := &fakeClock{now: time.Unix(1, 0)}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 3 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
		}
	}))
	defer server.Close()
	client := New(WithStatePath(path), WithClock(clock), WithInterval(0))
	if _, err := client.Get(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer other.Close()
	restarted := New(WithStatePath(path), WithClock(clock), WithInterval(0))
	clock.waits = nil
	if _, err := restarted.Get(context.Background(), other.URL); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 0 {
		t.Fatalf("other host waited: %v", clock.waits)
	}
	if _, err := restarted.Get(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 1 || clock.waits[0] != 30*time.Second {
		t.Fatalf("waits: %v", clock.waits)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state retryState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	host, _ := url.Parse(server.URL)
	if _, ok := state.Hosts[host.Host]; ok {
		t.Fatal("success did not clear record")
	}
}

func TestRetryStateExpiryAndInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	clock := &fakeClock{now: time.Unix(100000, 0)}
	client := New(WithStatePath(path), WithClock(clock))
	client.saveRetryState("old", hostRecord{RetryUntil: clock.now.Add(time.Hour), Streak: 1, UpdatedAt: clock.now.Add(-25 * time.Hour)})
	if len(client.loadRetryState().Hosts) != 0 {
		t.Fatal("expired state loaded")
	}
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(client.loadRetryState().Hosts) != 0 {
		t.Fatal("invalid state loaded")
	}
	client.saveRetryState("a", hostRecord{Streak: -1, UpdatedAt: clock.now})
	client.saveRetryState("b", hostRecord{Streak: 1, UpdatedAt: clock.now})
	state := client.loadRetryState()
	if state.Hosts["a"].Streak != 0 || state.Hosts["b"].Streak != 1 {
		t.Fatalf("state: %+v", state)
	}
}

func TestStatePath(t *testing.T) {
	dir := t.TempDir()
	path, err := StatePath(dir)
	if err != nil || path != filepath.Join(dir, "booxd", "fetch.json") {
		t.Fatalf("path %q: %v", path, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path created: %v", err)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err = StatePath("")
	if err != nil || path != filepath.Join(base, "booxd", "fetch.json") {
		t.Fatalf("default path %q: %v", path, err)
	}
}

func TestRetryStreakSurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		streak int
		want   time.Duration
	}{{1, 2 * time.Minute}, {10, 15 * time.Minute}} {
		t.Run(fmt.Sprint(tc.streak), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state")
			clock := &fakeClock{now: time.Unix(1, 0)}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(429)
				}
			}))
			defer server.Close()
			host, _ := url.Parse(server.URL)
			New(WithStatePath(path), WithClock(clock)).saveRetryState(host.Host, hostRecord{Streak: tc.streak, UpdatedAt: clock.now})
			client := New(WithStatePath(path), WithClock(clock), WithInterval(0))
			if _, err := client.Get(context.Background(), server.URL); err != nil {
				t.Fatal(err)
			}
			if len(clock.waits) != 1 || clock.waits[0] != tc.want {
				t.Fatalf("waits: %v", clock.waits)
			}
		})
	}
}

func TestCacheHitSkipsPersistedDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	clock := &fakeClock{now: time.Unix(1, 0)}
	client := New(WithStatePath(path), WithClock(clock), WithLookup(func(context.Context, string) (int, []byte, bool) { return http.StatusOK, []byte("cached"), true }))
	client.saveRetryState("example.test", hostRecord{RetryUntil: clock.now.Add(time.Hour), Streak: 1, UpdatedAt: clock.now})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Get(context.Background(), "http://example.test/book")
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Body) != "cached" || len(clock.waits) != 0 {
		t.Fatalf("result %q, waits %v", result.Body, clock.waits)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("cache hit changed state")
	}
	if _, ok := client.hosts.Load("example.test"); ok {
		t.Fatal("cache hit created host state")
	}
}
