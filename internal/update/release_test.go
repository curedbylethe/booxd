package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/curedbylethe/booxd/internal/fetch"
)

// releaseServer points latestReleaseURL at a fake GitHub endpoint and reports
// how many requests it answered.
func releaseServer(t *testing.T, status int, body string) (*fetch.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	previous := latestReleaseURL
	latestReleaseURL = server.URL
	t.Cleanup(func() { latestReleaseURL = previous })
	return fetch.New(fetch.WithInterval(0)), &calls
}

func TestLatestTag(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	tag, err := latestTag(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.2.4" || calls.Load() != 1 {
		t.Fatalf("tag = %q, requests = %d; want v1.2.4, 1", tag, calls.Load())
	}
}

func TestLatestTagErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", http.StatusNotFound, `{"tag_name":"v1.2.4"}`},
		{"bad JSON", http.StatusOK, `{"tag_name":`},
		{"empty tag", http.StatusOK, `{"tag_name":""}`},
		{"missing tag", http.StatusOK, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := releaseServer(t, tc.status, tc.body)
			if tag, err := latestTag(context.Background(), client); err == nil {
				t.Fatalf("tag = %q, error = nil; want a lookup error", tag)
			}
		})
	}
}
