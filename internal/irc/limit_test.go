package irc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateClock is a hand-advanced clock, so the gate tests never sleep. Wait
// advances it too, matching a real clock that moves forward while waiting.
type gateClock struct{ now time.Time }

func (c *gateClock) Now() time.Time { return c.now }

func (c *gateClock) Wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

func (c *gateClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// autoClock jumps an hour ahead on every read, so both send rules look expired
// to the gate. It is safe for concurrent senders.
type autoClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *autoClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Hour)
	return c.now
}

func (c *autoClock) Wait(ctx context.Context, d time.Duration) error { return ctx.Err() }

// gateClient starts a joined client with the gap and slot configured, and
// swaps in a fake clock at a fixed instant.
func gateClient(t *testing.T, gap, search time.Duration) (*Client, *fake, *gateClock) {
	t.Helper()
	f := newFake(t, welcome)
	cfg := f.config()
	cfg.MinSendGap = gap
	cfg.SearchTimeout = search
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	clk := &gateClock{now: time.Unix(1700000000, 0)}
	client.clock = clk
	f.collect(3) // NICK, USER, JOIN
	return client, f, clk
}

func TestSendGap(t *testing.T) {
	// The short search timeout keeps the slot from outliving each send, so the
	// gap is the only rule under test here.
	t.Run("boundary", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(9 * time.Second)
		err := client.Say(ctx, "bookbot", "two")
		if !errors.Is(err, ErrSendTooSoon) {
			t.Fatalf("Say at t0+9s = %v, want ErrSendTooSoon", err)
		}
		if !strings.Contains(err.Error(), "1s") {
			t.Fatalf("error %q does not name the remaining time", err)
		}
		if lines := f.drain(); len(lines) != 0 {
			t.Fatalf("rejected send wrote %q", lines)
		}

		clk.advance(time.Second)
		if err := client.Say(ctx, "bookbot", "three"); err != nil {
			t.Fatalf("Say at t0+10s: %v", err)
		}
		f.collect(1)
	})

	t.Run("rejected send leaves the gap unchanged", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(9 * time.Second)
		if err := client.Say(ctx, "bookbot", "two"); !errors.Is(err, ErrSendTooSoon) {
			t.Fatalf("Say at t0+9s = %v, want ErrSendTooSoon", err)
		}

		clk.advance(time.Second)
		if err := client.Say(ctx, "bookbot", "three"); err != nil {
			t.Fatalf("Say at t0+10s after a rejection: %v", err)
		}
		f.collect(1)
	})

	t.Run("zero gap disables the rule", func(t *testing.T) {
		client, f, _ := gateClient(t, 0, time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "one"); err != nil {
			t.Fatalf("first Say: %v", err)
		}
		// Release the slot so only the gap is under test.
		client.releaseSearch("bookbot")
		if err := client.Say(ctx, "bookbot", "two"); err != nil {
			t.Fatalf("second Say with no gap: %v", err)
		}
		f.collect(2)
	})

	t.Run("PING is not gated", func(t *testing.T) {
		client, f, _ := gateClient(t, 10*time.Second, 30*time.Second)

		if err := client.Say(context.Background(), "bookbot", "one"); err != nil {
			t.Fatalf("Say: %v", err)
		}
		f.collect(1)

		f.write("PING :tok123\r\n")
		if lines := f.collect(1); strings.TrimSpace(lines[0]) != "PONG :tok123" {
			t.Fatalf("fake saw %q, want the PONG", lines[0])
		}
	})
}

func TestSearchSlot(t *testing.T) {
	t.Run("occupied until the reply", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		if err := client.Say(ctx, "bookbot", "search two"); !errors.Is(err, ErrSearchPending) {
			t.Fatalf("Say at t0+10s = %v, want ErrSearchPending", err)
		}
		if lines := f.drain(); len(lines) != 0 {
			t.Fatalf("rejected send wrote %q", lines)
		}

		f.write(":bookbot!u@h PRIVMSG booxd :no results\r\n")
		nextMessage(t, client.Messages()) // the reader has released the slot

		if err := client.Say(ctx, "bookbot", "search three"); err != nil {
			t.Fatalf("Say after the reply: %v", err)
		}
		f.collect(1)
	})

	t.Run("NOTICE releases the slot", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		f.write(":bookbot!u@h NOTICE booxd :search unavailable\r\n")
		nextMessage(t, client.Messages())

		if err := client.Say(ctx, "bookbot", "search two"); err != nil {
			t.Fatalf("Say after the notice: %v", err)
		}
		f.collect(1)
	})

	t.Run("timeout releases the slot", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		if err := client.Say(ctx, "bookbot", "search two"); !errors.Is(err, ErrSearchPending) {
			t.Fatalf("Say at t0+10s = %v, want ErrSearchPending", err)
		}

		clk.advance(21 * time.Second)
		if err := client.Say(ctx, "bookbot", "search three"); err != nil {
			t.Fatalf("Say at t0+31s: %v", err)
		}
		f.collect(1)
	})

	t.Run("another nick does not release", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		f.write(":otherbot!u@h PRIVMSG booxd :unrelated\r\n")
		nextMessage(t, client.Messages())

		if err := client.Say(ctx, "bookbot", "search two"); !errors.Is(err, ErrSearchPending) {
			t.Fatalf("Say after another nick spoke = %v, want ErrSearchPending", err)
		}
	})

	t.Run("offer releases the slot", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		// STAGE-06 calls this for a DCC offer from the same nick.
		client.releaseSearch("bookbot")
		if err := client.Say(ctx, "bookbot", "search two"); err != nil {
			t.Fatalf("Say after an offer from the search bot: %v", err)
		}
		f.collect(1)
	})

	t.Run("offer from another nick does not release", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 30*time.Second)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(10 * time.Second)
		client.releaseSearch("otherbot")
		if err := client.Say(ctx, "bookbot", "search two"); !errors.Is(err, ErrSearchPending) {
			t.Fatalf("Say after an offer from another nick = %v, want ErrSearchPending", err)
		}
	})

	t.Run("zero timeout only releases on the reply", func(t *testing.T) {
		client, f, clk := gateClient(t, 10*time.Second, 0)
		ctx := context.Background()

		if err := client.Say(ctx, "bookbot", "search one"); err != nil {
			t.Fatalf("Say at t0: %v", err)
		}
		f.collect(1)

		clk.advance(time.Hour)
		if err := client.Say(ctx, "bookbot", "search two"); !errors.Is(err, ErrSearchPending) {
			t.Fatalf("Say an hour later = %v, want ErrSearchPending", err)
		}

		client.releaseSearch("bookbot")
		if err := client.Say(ctx, "bookbot", "search three"); err != nil {
			t.Fatalf("Say after the reply: %v", err)
		}
		f.collect(1)
	})
}
