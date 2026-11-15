package irc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDial(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		if got := client.Nick(); got != "booxd" {
			t.Fatalf("Nick() = %q, want %q", got, "booxd")
		}
		if got := client.Channel(); got != "#ebooks" {
			t.Fatalf("Channel() = %q, want %q", got, "#ebooks")
		}
	})

	t.Run("433 once", func(t *testing.T) {
		f := newFake(t, func(line string) []string {
			switch {
			case strings.HasPrefix(line, "NICK booxd_"):
				return []string{":srv 001 booxd_ :welcome"}
			case strings.HasPrefix(line, "NICK "):
				return []string{":srv 433 * booxd :Nickname is already in use"}
			case strings.HasPrefix(line, "JOIN "):
				return []string{":srv 366 booxd_ #ebooks :End of /NAMES"}
			}
			return nil
		})
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		if got := client.Nick(); got != "booxd_" {
			t.Fatalf("Nick() = %q, want %q", got, "booxd_")
		}
		lines := f.collect(4)
		if n := countPrefix(lines, "NICK "); n != 2 {
			t.Fatalf("fake saw %d NICK lines, want 2: %q", n, lines)
		}
	})

	t.Run("433 three times", func(t *testing.T) {
		f := newFake(t, func(line string) []string {
			if strings.HasPrefix(line, "NICK ") {
				return []string{":srv 433 * booxd :Nickname is already in use"}
			}
			return nil
		})
		_, err := Dial(context.Background(), f.config())
		if err == nil {
			t.Fatal("Dial succeeded after three 433s")
		}
		if !strings.Contains(err.Error(), "booxd__") {
			t.Fatalf("error %q does not name the rejected nick", err)
		}
		if n := countPrefix(f.collect(4), "NICK "); n != 3 {
			t.Fatalf("fake saw %d NICK lines, want 3", n)
		}
	})

	t.Run("zero dial timeout", func(t *testing.T) {
		f := newFake(t, welcome)
		cfg := f.config()
		cfg.DialTimeout = 0
		client, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Dial with no dial timeout: %v", err)
		}
		defer func() { _ = client.Close() }()
	})

	t.Run("ping before 001", func(t *testing.T) {
		f := newFake(t, func(line string) []string {
			switch {
			case strings.HasPrefix(line, "NICK "):
				return []string{"PING :cookie"}
			case strings.HasPrefix(line, "PONG :cookie"):
				return []string{":srv 001 booxd :welcome"}
			case strings.HasPrefix(line, "JOIN "):
				return []string{":srv 366 booxd #ebooks :End of /NAMES"}
			}
			return nil
		})
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		if n := countPrefix(f.collect(4), "PONG :cookie"); n != 1 {
			t.Fatal("fake did not see the PONG for the registration cookie")
		}
	})

	t.Run("registration timeout", func(t *testing.T) {
		f := newFake(t, nil)
		cfg := f.config()
		cfg.RegisterTimeout = 100 * time.Millisecond
		_, err := Dial(context.Background(), cfg)
		if err == nil {
			t.Fatal("Dial succeeded without 001")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error %q does not wrap context.DeadlineExceeded", err)
		}
	})

	for _, tc := range []struct {
		numeric string
		reason  string
	}{
		{"473", "invite only"},
		{"474", "banned"},
		{"475", "bad key"},
	} {
		t.Run("join "+tc.numeric, func(t *testing.T) {
			f := newFake(t, func(line string) []string {
				switch {
				case strings.HasPrefix(line, "USER "):
					return []string{":srv 001 booxd :welcome"}
				case strings.HasPrefix(line, "JOIN "):
					return []string{":srv " + tc.numeric + " booxd #ebooks :refused"}
				}
				return nil
			})
			_, err := Dial(context.Background(), f.config())
			if err == nil {
				t.Fatalf("Dial succeeded on %s", tc.numeric)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error %q does not say %q", err, tc.reason)
			}
			if !strings.Contains(err.Error(), "#ebooks") {
				t.Fatalf("error %q does not name the channel", err)
			}
		})
	}

	t.Run("unrelated 422 before 366", func(t *testing.T) {
		f := newFake(t, func(line string) []string {
			switch {
			case strings.HasPrefix(line, "USER "):
				return []string{":srv 001 booxd :welcome"}
			case strings.HasPrefix(line, "JOIN "):
				return []string{":srv 422 booxd :MOTD File is missing", ":srv 366 booxd #ebooks :End of /NAMES"}
			}
			return nil
		})
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial with a 422 in the welcome burst: %v", err)
		}
		defer func() { _ = client.Close() }()
	})
}

func TestDialCancelDuringRegistration(t *testing.T) {
	// A server that accepts the connection and then says nothing.
	f := newFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Dial(ctx, f.config())
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Dial took %s after cancellation, want under 1s", elapsed)
	}
	f.collect(2) // NICK, USER
}

func TestDialTLS(t *testing.T) {
	f := newFake(t, welcome)
	f.cert = selfSigned(t)
	cfg := f.config()
	cfg.TLS = true
	cfg.InsecureTLS = true

	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial with InsecureTLS: %v", err)
	}
	_ = client.Close()

	cfg.InsecureTLS = false
	if _, err := Dial(context.Background(), cfg); err == nil {
		t.Fatal("Dial verified a self-signed certificate")
	}
}

func TestSay(t *testing.T) {
	t.Run("serialized", func(t *testing.T) {
		f := newFake(t, welcome)
		cfg := f.config()
		// The send limiter is not under test here, so a clock that always jumps
		// ahead keeps both the gap and the slot expired; the senders then only
		// contend for the writer.
		cfg.SearchTimeout = time.Second
		client, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		client.clock = &autoClock{now: time.Unix(1700000000, 0)}
		f.collect(3) // NICK, USER, JOIN

		const senders = 20
		var wg sync.WaitGroup
		for i := 0; i < senders; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := client.Say(context.Background(), "#ebooks", fmt.Sprintf("book %d", i)); err != nil {
					t.Errorf("Say %d: %v", i, err)
				}
			}(i)
		}
		wg.Wait()

		lines := f.collect(senders)
		for _, line := range lines {
			if !strings.HasSuffix(line, "\r\n") {
				t.Fatalf("line %q does not end in CRLF", line)
			}
			if !strings.HasPrefix(line, "PRIVMSG") {
				t.Fatalf("line %q does not start with PRIVMSG", line)
			}
			if n := strings.Count(line, "PRIVMSG"); n != 1 {
				t.Fatalf("line %q holds %d PRIVMSG prefixes", line, n)
			}
		}
	})

	t.Run("rejects injection", func(t *testing.T) {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		f.collect(3)

		if err := client.Say(context.Background(), "#ebooks", "\nQUIT"); err == nil {
			t.Fatal("Say accepted a newline")
		}
		if err := client.CTCP(context.Background(), "#ebooks", "VERSION\r\nQUIT"); err == nil {
			t.Fatal("CTCP accepted a carriage return")
		}
		if lines := f.drain(); len(lines) != 0 {
			t.Fatalf("fake read %q after rejected sends", lines)
		}
	})
}

func TestClose(t *testing.T) {
	f := newFake(t, welcome)
	client, err := Dial(context.Background(), f.config())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	f.collect(3)

	if err := client.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !errors.Is(client.Err(), ErrClosed) {
		t.Fatalf("Err() = %v, want ErrClosed", client.Err())
	}

	lines := f.collect(1)
	if got := strings.TrimSpace(lines[0]); got != "QUIT" {
		t.Fatalf("fake saw %q, want QUIT", got)
	}
	if extra := f.drain(); len(extra) != 0 {
		t.Fatalf("Close wrote twice: %q", extra)
	}
}

// countPrefix counts recorded lines that start with prefix.
func countPrefix(lines []string, prefix string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}
