package fetch

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryWait(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		header string
		streak int
		want   time.Duration
	}{
		{"", 1, time.Minute}, {"", 2, 2 * time.Minute}, {"", 3, 4 * time.Minute}, {"", 5, 15 * time.Minute}, {"", 10, 15 * time.Minute},
		{"2", 1, 2 * time.Second}, {now.Add(3 * time.Second).Format(http.TimeFormat), 1, 3 * time.Second}, {"0", 1, 0}, {"-5", 1, time.Minute}, {"nonsense", 1, time.Minute}, {"86400", 1, 15 * time.Minute},
	} {
		t.Run(tt.header+time.Duration(tt.streak).String(), func(t *testing.T) {
			h := make(http.Header)
			h.Set("Retry-After", tt.header)
			if got := retryWait(h, tt.streak, now); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChallenge(t *testing.T) {
	for _, tt := range []struct {
		name   string
		header http.Header
		body   string
		want   bool
	}{
		{"server cloudflare", http.Header{"Server": {"Cloudflare"}}, "", true}, {"server ddos", http.Header{"Server": {"DDoS-Guard"}}, "", true},
		{"ray", http.Header{"Cf-Ray": {"abc"}}, "", true}, {"mitigated", http.Header{"Cf-Mitigated": {"challenge"}}, "", true},
		{"cookie", http.Header{"Set-Cookie": {"__ddgid=x"}}, "", true}, {"body ddos", nil, "DDoS-Guard", true}, {"body moment", nil, "Just a moment...", true}, {"plain", nil, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, got := challenge(&Response{StatusCode: 403, Header: tt.header, Body: []byte(tt.body)})
			if got != tt.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
