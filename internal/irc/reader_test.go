package irc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReadLines(t *testing.T) {
	f := newFake(t, welcome)
	client, err := Dial(context.Background(), f.config())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	f.collect(3) // NICK, USER, JOIN

	f.write(":srv PRIVMSG #ebooks :one\r\n:srv PRIVMSG #ebooks :two\n")

	first := nextMessage(t, client.Messages())
	if first.Command != "PRIVMSG" || first.Trailing != "one" {
		t.Fatalf("first message = %+v, want PRIVMSG one", first)
	}
	if first.Raw != ":srv PRIVMSG #ebooks :one" {
		t.Fatalf("first Raw = %q, want the line without CRLF", first.Raw)
	}
	second := nextMessage(t, client.Messages())
	if second.Command != "PRIVMSG" || second.Trailing != "two" {
		t.Fatalf("second message = %+v, want PRIVMSG two", second)
	}
}

func TestReadLineTooLong(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"newline after the cap", strings.Repeat("x", 4097) + "\r\n"},
		{"no newline at all", strings.Repeat("x", 4098)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, welcome)
			client, err := Dial(context.Background(), f.config())
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			f.collect(3)

			f.writeBestEffort(tt.raw)

			if err := waitErr(t, client); !errors.Is(err, ErrLineTooLong) {
				t.Fatalf("Err() = %v, want ErrLineTooLong", err)
			}
			waitClosed(t, client.Messages())
			if lines := f.drain(); len(lines) != 0 {
				t.Fatalf("fake saw %q after an over-long line, want nothing", lines)
			}
		})
	}
}

func TestDeliverAfterShutdown(t *testing.T) {
	c := &Client{done: make(chan struct{}), msgs: make(chan Message, 1)}
	close(c.done)
	close(c.msgs)

	// A reader that lost the race with Close must not send on a closed channel.
	deliver(c, c.msgs, Message{}, "messages")
}

func TestCloseWhileReading(t *testing.T) {
	for range 300 {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		f.flood(":srv PRIVMSG booxd :line\r\n")
		if err := client.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		waitClosed(t, client.Messages())
	}
}

func TestPing(t *testing.T) {
	f := newFake(t, welcome)
	client, err := Dial(context.Background(), f.config())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	f.collect(3)

	f.write("PING :tok123\r\n")

	lines := f.collect(1)
	if got := strings.TrimSpace(lines[0]); got != "PONG :tok123" {
		t.Fatalf("fake saw %q, want %q", got, "PONG :tok123")
	}
	select {
	case msg := <-client.Messages():
		t.Fatalf("PING was delivered as %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestReadError(t *testing.T) {
	t.Run("ERROR", func(t *testing.T) {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		f.collect(3)

		f.write("ERROR :Closing link\r\n")

		if err := waitErr(t, client); err == nil || !strings.Contains(err.Error(), "Closing link") {
			t.Fatalf("Err() = %v, want the server's reason", err)
		}
		waitClosed(t, client.Messages())
	})

	t.Run("EOF", func(t *testing.T) {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		f.collect(3)

		f.closeAll()

		if err := waitErr(t, client); err == nil {
			t.Fatal("Err() = nil after the server closed the pipe")
		}
		waitClosed(t, client.Messages())
	})
}

func TestReadIdle(t *testing.T) {
	t.Run("silent", func(t *testing.T) {
		f := newFake(t, welcome)
		cfg := f.config()
		cfg.IdleTimeout = 150 * time.Millisecond
		client, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()

		err = waitErr(t, client)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Err() = %v, want a deadline error", err)
		}
		if !strings.Contains(err.Error(), "connection idle") {
			t.Fatalf("Err() = %v, want it to name the idle timeout", err)
		}
		waitClosed(t, client.Messages())
	})

	t.Run("refreshed", func(t *testing.T) {
		f := newFake(t, welcome)
		cfg := f.config()
		cfg.IdleTimeout = 150 * time.Millisecond
		client, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()

		for i := 0; i < 3; i++ {
			<-time.After(40 * time.Millisecond)
			f.write(":srv PRIVMSG #ebooks :keep\r\n")
		}
		for i := 0; i < 3; i++ {
			if msg := nextMessage(t, client.Messages()); msg.Trailing != "keep" {
				t.Fatalf("message %d = %+v, want trailing %q", i, msg, "keep")
			}
		}
		if err := client.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil while lines keep arriving", err)
		}
	})
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFake(t, welcome)
	client, err := Dial(ctx, f.config())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	f.collect(3)

	cancel()

	if err := waitErr(t, client); err == nil {
		t.Fatal("Err() = nil after cancellation")
	}
	if lines := f.collect(1); strings.TrimSpace(lines[0]) != "QUIT" {
		t.Fatalf("fake saw %q, want QUIT", lines[0])
	}
	waitClosed(t, client.Messages())
	waitClosed(t, client.DCC())
}

func TestReadGoroutineExits(t *testing.T) {
	baseline := runtime.NumGoroutine()

	f := newFake(t, welcome)
	client, err := Dial(context.Background(), f.config())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, baseline %d after Close", runtime.NumGoroutine(), baseline)
		}
		<-time.After(10 * time.Millisecond)
	}
}

func TestTrace(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		f := newFake(t, welcome)
		cfg := f.config()
		var buf bytes.Buffer
		cfg.Trace = &buf
		client, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		f.collect(3)

		f.write(":srv PRIVMSG #ebooks :hi\r\n")
		nextMessage(t, client.Messages())
		f.write("PING :tok123\r\n")
		f.collect(1)

		// Close stops the reader before the buffer is read.
		_ = client.Close()
		trace := buf.String()
		if !strings.Contains(trace, "irc: <- :srv PRIVMSG #ebooks :hi") {
			t.Fatalf("trace %q does not hold the inbound line", trace)
		}
		if !strings.Contains(trace, "irc: -> PONG :tok123") {
			t.Fatalf("trace %q does not hold the PONG", trace)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		f := newFake(t, welcome)
		client, err := Dial(context.Background(), f.config()) // Trace nil
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		f.collect(3)

		f.write("PING :tok123\r\n")

		if lines := f.collect(1); strings.TrimSpace(lines[0]) != "PONG :tok123" {
			t.Fatalf("fake saw %q, want the PONG", lines[0])
		}
		if extra := f.drain(); len(extra) != 0 {
			t.Fatalf("fake saw %q, want only the PONG", extra)
		}
	})
}

// nextMessage waits for one Message.
func nextMessage(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("Messages() closed early")
		}
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("no message within 2s")
		return Message{}
	}
}

// waitClosed waits for a channel to close.
func waitClosed[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel still open after 2s")
		}
	}
}

// waitErr waits up to one second for a terminal error.
func waitErr(t *testing.T, c *Client) error {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if err := c.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatal("Err() still nil after 1s")
		}
		<-time.After(10 * time.Millisecond)
	}
}
