package mirror

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestResolve(t *testing.T) {
	now := time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		statuses []int
		age      time.Duration
		dead     []time.Duration
		corrupt  bool
		selected int
		calls    []int32
		failure  string
		retry    bool
	}{
		{name: "fresh reuse", statuses: []int{200}, age: 23 * time.Hour, calls: []int32{0}},
		{name: "24-hour boundary", statuses: []int{200}, age: 24 * time.Hour, calls: []int32{1}},
		{name: "stale", statuses: []int{200}, age: 25 * time.Hour, calls: []int32{1}},
		{name: "first pass and dead mark", statuses: []int{403, 200}, selected: 1, calls: []int32{1, 1}},
		{name: "404 also dead", statuses: []int{404, 200}, selected: 1, calls: []int32{1, 1}},
		{name: "skip dead", statuses: []int{200, 200}, dead: []time.Duration{30 * time.Minute}, selected: 1, calls: []int32{0, 1}},
		{name: "dead expired", statuses: []int{200}, dead: []time.Duration{-time.Minute}, calls: []int32{1}},
		{name: "cooldown boundary", statuses: []int{200}, dead: []time.Duration{0}, calls: []int32{1}},
		{name: "429 is not dead", statuses: []int{429, 200}, selected: 1, calls: []int32{1, 1}},
		{name: "all fail", statuses: []int{403, 500}, age: 25 * time.Hour, selected: -1, calls: []int32{1, 1}, failure: "tried"},
		{name: "all dead persist", statuses: []int{403, 404}, selected: -1, calls: []int32{1, 1}, failure: "tried", retry: true},
		{name: "all cooling down", statuses: []int{200, 200}, dead: []time.Duration{30 * time.Minute, 30 * time.Minute}, selected: -1, calls: []int32{0, 0}, failure: "cooling"},
		{name: "corrupt state", statuses: []int{200}, corrupt: true, calls: []int32{1}},
		{name: "order preserved", statuses: []int{200, 200}, calls: []int32{1, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mirror.json")
			seeds := make([]string, len(tt.statuses))
			calls := make([]atomic.Int32, len(tt.statuses))
			for i, status := range tt.statuses {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls[i].Add(1)
					if r.Method != http.MethodGet || r.URL.RequestURI() != probePath {
						t.Errorf("unexpected probe: %s %s", r.Method, r.URL.RequestURI())
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(passMarker))
				}))
				t.Cleanup(server.Close)
				seeds[i] = server.URL
			}
			initial := state{DeadUntil: make(map[string]time.Time)}
			if tt.age != 0 {
				initial.BaseURL, initial.CheckedAt = seeds[0], now.Add(-tt.age)
			}
			for i, offset := range tt.dead {
				initial.DeadUntil[seeds[i]] = now.Add(offset)
			}
			if tt.age != 0 || tt.dead != nil {
				if err := saveState(path, initial); err != nil {
					t.Fatal(err)
				}
			}
			if tt.corrupt {
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input := append([]string(nil), seeds...)
			for i := range input {
				input[i] += "/"
			}
			got, err := Resolve(context.Background(), fetch.New(), path, input, now)
			if tt.selected >= 0 {
				if err != nil || got != seeds[tt.selected] {
					t.Fatalf("Resolve = %q, %v; want %q", got, err, seeds[tt.selected])
				}
			} else {
				if err == nil || got != "" {
					t.Fatalf("Resolve = %q, %v; want failure", got, err)
				}
				if tt.failure == "cooling" && !strings.Contains(err.Error(), "every seed is cooling down") {
					t.Fatalf("error = %v, want cooldown explanation", err)
				}
				if tt.failure == "tried" {
					var tried []string
					for i, seed := range seeds {
						label := "fail"
						if tt.statuses[i] == 403 || tt.statuses[i] == 404 {
							label = "dead"
						}
						tried = append(tried, seed+" ("+label+")")
					}
					if want := "no working mirror: " + strings.Join(tried, ", "); err.Error() != want {
						t.Fatalf("error = %v, want %q", err, want)
					}
				}
			}
			stored := loadState(path)
			if tt.selected >= 0 {
				checked := now
				if tt.name == "fresh reuse" {
					checked = initial.CheckedAt
				}
				if stored.BaseURL != got || !stored.CheckedAt.Equal(checked) {
					t.Fatalf("stored state = %#v, want base %q and timestamp %v", stored, got, checked)
				}
			} else if stored.BaseURL != "" || !stored.CheckedAt.IsZero() {
				t.Fatalf("failed discovery retained selection: %#v", stored)
			}
			for i, status := range tt.statuses {
				if calls[i].Load() != tt.calls[i] {
					t.Fatalf("seed %d requests = %d, want %d", i, calls[i].Load(), tt.calls[i])
				}
				if tt.calls[i] > 0 && (status == 403 || status == 404) && !stored.DeadUntil[seeds[i]].Equal(now.Add(time.Hour)) {
					t.Fatalf("seed %d deadline = %v, want now+1h", i, stored.DeadUntil[seeds[i]])
				}
				if status == 429 && !stored.DeadUntil[seeds[i]].IsZero() {
					t.Fatal("429 marked seed dead")
				}
			}
			if tt.retry {
				_, err = Resolve(context.Background(), fetch.New(), path, seeds, now.Add(30*time.Minute))
				if err == nil || !strings.Contains(err.Error(), "every seed is cooling down") {
					t.Fatalf("second discovery error = %v", err)
				}
				for i := range calls {
					if calls[i].Load() != tt.calls[i] {
						t.Fatal("second discovery ignored persisted cooldown")
					}
				}
			}
		})
	}
}

func TestResolveCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirror.json")
	before := []byte(`{"base_url":"https://old.test","checked_at":"2000-01-01T00:00:00Z"}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := Resolve(ctx, fetch.New(), path, []string{server.URL}, time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC))
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve did not return after cancellation")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("cancelled run changed state to %q, error = %v", after, err)
	}
}

func TestResolveCancelledPassingResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirror.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := http.DefaultTransport
	http.DefaultTransport = probeTransportFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(passMarker))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	_, err := Resolve(ctx, fetch.New(), path, []string{"http://mirror.test"}, time.Time{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled passing probe wrote state: %v", err)
	}
}

func TestResolveSaveError(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(passMarker))
			}))
			t.Cleanup(server.Close)
			got, err := Resolve(context.Background(), fetch.New(), t.TempDir(), []string{server.URL}, time.Time{})
			if got != "" || err == nil || !strings.Contains(err.Error(), "save mirror state") {
				t.Fatalf("Resolve = %q, %v; want save error", got, err)
			}
		})
	}
}
