package mirror

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestMirrorStateMachine(t *testing.T) {
	newClock := func() *probeClock {
		return &probeClock{now: time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)}
	}

	t.Run("healthy", func(t *testing.T) {
		clock := newClock()
		first, firstCalls := startMirror(t, fixedResponse(http.StatusOK))
		second, secondCalls := startMirror(t, fixedResponse(http.StatusOK))
		path := t.TempDir() + "/mirror.json"
		now := clock.Now()

		got, err := Resolve(context.Background(), fetch.New(fetch.WithClock(clock)), path, []string{first, second}, now)
		if err != nil || got != first {
			t.Fatalf("Resolve() = %q, %v; want %q, nil", got, err, first)
		}
		if firstCalls.Load() != 1 || secondCalls.Load() != 0 {
			t.Fatalf("requests = (%d, %d), want (1, 0)", firstCalls.Load(), secondCalls.Load())
		}
		st := loadState(path)
		if st.BaseURL != first || !st.CheckedAt.Equal(now) || len(st.DeadUntil) != 0 {
			t.Fatalf("state = %+v, want first selection at %v with no cooldowns", st, now)
		}
	})

	t.Run("403 cooldown window", func(t *testing.T) {
		clock := newClock()
		var status atomic.Int32
		status.Store(http.StatusForbidden)
		first, firstCalls := startMirror(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(int(status.Load()))
			if status.Load() == http.StatusOK {
				_, _ = w.Write([]byte(passMarker))
			}
		})
		second, secondCalls := startMirror(t, fixedResponse(http.StatusOK))
		path := t.TempDir() + "/mirror.json"
		t0 := clock.Now()
		client := fetch.New(fetch.WithClock(clock))

		got, err := resolve(context.Background(), client, path, []string{first, second}, clock.Now(), probeTimeout)
		if err != nil || got != second {
			t.Fatalf("first Resolve() = %q, %v; want %q, nil", got, err, second)
		}
		if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
			t.Fatalf("first requests = (%d, %d), want (1, 1)", firstCalls.Load(), secondCalls.Load())
		}
		if got := loadState(path).DeadUntil[first]; !got.Equal(t0.Add(time.Hour)) {
			t.Fatalf("cooldown = %v, want %v", got, t0.Add(time.Hour))
		}

		ageSelection(t, path, clock.Now())
		clock.Advance(59 * time.Minute)
		got, err = resolve(context.Background(), client, path, []string{first, second}, clock.Now(), probeTimeout)
		if err != nil || got != second {
			t.Fatalf("cooldown Resolve() = %q, %v; want %q, nil", got, err, second)
		}
		if firstCalls.Load() != 1 || secondCalls.Load() != 2 {
			t.Fatalf("cooldown requests = (%d, %d), want (1, 2)", firstCalls.Load(), secondCalls.Load())
		}

		ageSelection(t, path, clock.Now())
		status.Store(http.StatusOK)
		clock.Advance(2 * time.Minute)
		got, err = resolve(context.Background(), client, path, []string{first, second}, clock.Now(), probeTimeout)
		if err != nil || got != first {
			t.Fatalf("recovery Resolve() = %q, %v; want %q, nil", got, err, first)
		}
		if firstCalls.Load() != 2 || secondCalls.Load() != 2 {
			t.Fatalf("recovery requests = (%d, %d), want (2, 2)", firstCalls.Load(), secondCalls.Load())
		}
		if got := loadState(path).DeadUntil[first]; !got.Equal(t0.Add(time.Hour)) {
			t.Fatalf("persisted cooldown = %v, want %v", got, t0.Add(time.Hour))
		}
	})

	t.Run("429 recovers", func(t *testing.T) {
		clock := newClock()
		var firstCalls atomic.Int32
		firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if firstCalls.Add(1) <= 2 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(passMarker))
		}))
		t.Cleanup(firstServer.Close)
		second, secondCalls := startMirror(t, fixedResponse(http.StatusOK))
		path := t.TempDir() + "/mirror.json"
		now := clock.Now()

		got, err := Resolve(context.Background(), fetch.New(fetch.WithClock(clock)), path, []string{firstServer.URL, second}, now)
		if err != nil || got != firstServer.URL {
			t.Fatalf("Resolve() = %q, %v; want %q, nil", got, err, firstServer.URL)
		}
		if firstCalls.Load() != 3 || secondCalls.Load() != 0 {
			t.Fatalf("requests = (%d, %d), want (3, 0)", firstCalls.Load(), secondCalls.Load())
		}
		if got := loadState(path).DeadUntil[firstServer.URL]; !got.IsZero() {
			t.Fatalf("cooldown = %v, want none", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		clock := newClock()
		first, firstCalls := startMirror(t, func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		second, secondCalls := startMirror(t, fixedResponse(http.StatusOK))
		path := t.TempDir() + "/mirror.json"
		now := clock.Now()

		got, err := resolve(context.Background(), fetch.New(fetch.WithClock(clock)), path, []string{first, second}, now, 50*time.Millisecond)
		if err != nil || got != second {
			t.Fatalf("resolve() = %q, %v; want %q, nil", got, err, second)
		}
		if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
			t.Fatalf("requests = (%d, %d), want (1, 1)", firstCalls.Load(), secondCalls.Load())
		}
		if got := loadState(path).DeadUntil[first]; !got.Equal(now.Add(time.Hour)) {
			t.Fatalf("cooldown = %v, want %v", got, now.Add(time.Hour))
		}
	})
}

func startMirror(t *testing.T, handler http.HandlerFunc) (string, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL, calls
}

func fixedResponse(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(passMarker))
		}
	}
}

func ageSelection(t *testing.T, path string, now time.Time) {
	t.Helper()
	st := loadState(path)
	st.CheckedAt = now.Add(-25 * time.Hour)
	if err := saveState(path, st); err != nil {
		t.Fatalf("age selection: %v", err)
	}
}
