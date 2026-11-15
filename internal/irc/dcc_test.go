package irc

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// Test addresses: 3232235777 is 192.168.1.1 (private), 3405803785 is
// 203.0.113.9 (public).
const (
	dccPrivate = "3232235777"
	dccPublic  = "3405803785"
	dccHost    = "203.0.113.9"
)

// syncBuffer is a trace writer safe to read while the reader still writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// dccClient starts a joined client whose trace goes to a safe buffer.
func dccClient(t *testing.T) (*Client, *fake, *syncBuffer) {
	t.Helper()
	f := newFake(t, welcome)
	cfg := f.config()
	trace := &syncBuffer{}
	cfg.Trace = trace
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	f.collect(3) // NICK, USER, JOIN
	return client, f, trace
}

func TestDCCSend(t *testing.T) {
	want := dcc.Offer{From: "bot", Name: "A Book.epub", IP: net.ParseIP(dccHost), Port: 5000, Size: 1234}
	cases := []struct {
		name string
		line string
		verb string
		body string
	}{
		{
			name: "quoted",
			line: `:bot!b@203.0.113.9 PRIVMSG booxd :` + "\x01" + `DCC SEND "A Book.epub" 3232235777 5000 1234` + "\x01",
			verb: "PRIVMSG",
			body: "\x01" + `DCC SEND "A Book.epub" 3232235777 5000 1234` + "\x01",
		},
		{
			name: "unquoted",
			line: `:bot!b@203.0.113.9 PRIVMSG booxd :` + "\x01" + `DCC SEND A Book.epub 3232235777 5000 1234` + "\x01",
			verb: "PRIVMSG",
			body: "\x01" + `DCC SEND A Book.epub 3232235777 5000 1234` + "\x01",
		},
		{
			name: "notice",
			line: `:bot!b@203.0.113.9 NOTICE booxd :` + "\x01" + `DCC SEND "A Book.epub" 3232235777 5000 1234` + "\x01",
			verb: "NOTICE",
			body: "\x01" + `DCC SEND "A Book.epub" 3232235777 5000 1234` + "\x01",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, f, _ := dccClient(t)
			f.write(tc.line + "\r\n")

			assertOffer(t, nextOffer(t, client.DCC()), want)
			msg := nextMessage(t, client.Messages())
			if msg.Command != tc.verb || msg.Trailing != tc.body {
				t.Fatalf("message = %+v, want %s %q", msg, tc.verb, tc.body)
			}
		})
	}
}

func TestDCCPublicIP(t *testing.T) {
	client, f, _ := dccClient(t)
	// A public integer address is kept; the prefix host is not applied.
	f.write(`:bot!b@198.51.100.7 PRIVMSG booxd :` + "\x01" +
		`DCC SEND "A Book.epub" ` + dccPublic + ` 5000 1234` + "\x01\r\n")

	assertOffer(t, nextOffer(t, client.DCC()), dcc.Offer{
		From: "bot", Name: "A Book.epub", IP: net.ParseIP(dccHost), Port: 5000, Size: 1234,
	})
	nextMessage(t, client.Messages())
}

func TestDCCPrivateNoHost(t *testing.T) {
	client, f, trace := dccClient(t)
	f.write(`:bot!b@ PRIVMSG booxd :` + "\x01" +
		`DCC SEND "A Book.epub" ` + dccPrivate + ` 5000 1234` + "\x01\r\n")
	f.write(":srv PRIVMSG booxd :still here\r\n")

	nextMessage(t, client.Messages()) // the CTCP line still arrives
	if msg := nextMessage(t, client.Messages()); msg.Trailing != "still here" {
		t.Fatalf("message after the bad offer = %+v, want the normal line", msg)
	}
	noOffer(t, client.DCC())
	if n := countTrace(trace.String(), "dcc offer from bot"); n != 1 {
		t.Fatalf("traces naming the bad offer = %d, want 1: %q", n, trace.String())
	}
	if err := client.Err(); err != nil {
		t.Fatalf("Err() = %v, want the session still open", err)
	}
}

func TestDCCNotAnOffer(t *testing.T) {
	client, f, trace := dccClient(t)
	lines := []string{
		`:bot!b@h PRIVMSG booxd :` + "\x01" + `VERSION` + "\x01",
		`:bot!b@h PRIVMSG booxd :hello`,
		`:bot!b@h PRIVMSG booxd :` + "\x01" + `DCC CHAT chat ` + dccPrivate + ` 5000` + "\x01",
		`:bot!b@h PRIVMSG booxd :` + "\x01" + `DCC RESUME "x" 5000 100` + "\x01",
	}
	for _, line := range lines {
		f.write(line + "\r\n")
	}
	for i, line := range lines {
		if msg := nextMessage(t, client.Messages()); msg.Raw != line {
			t.Fatalf("message %d = %+v, want the line verbatim", i, msg)
		}
	}
	noOffer(t, client.DCC())
	if n := countTrace(trace.String(), "dcc offer"); n != 0 {
		t.Fatalf("traces for non-offers = %d, want 0: %q", n, trace.String())
	}
}

func TestDCCChannelOffer(t *testing.T) {
	client, f, trace := dccClient(t)
	line := `:stranger!s@198.51.100.7 PRIVMSG #ebooks :` + "\x01" +
		`DCC SEND x 16843009 80 1` + "\x01"
	f.write(line + "\r\n")

	nextMessage(t, client.Messages())
	noOffer(t, client.DCC())
	if n := countTrace(trace.String(), "dcc offer"); n != 0 {
		t.Fatalf("traces for a channel offer = %d, want 0: %q", n, trace.String())
	}
}

func TestDCCFull(t *testing.T) {
	client, f, trace := dccClient(t)
	var script strings.Builder
	for i := 0; i < 17; i++ {
		fmt.Fprintf(&script, `:bot!b@203.0.113.9 PRIVMSG booxd :`+"\x01"+
			`DCC SEND book%d.epub `+dccPrivate+` 5000 1234`+"\x01\r\n", i)
	}
	f.write(script.String())
	// Wait for the 17th offer to be dropped, so every earlier line is queued.
	waitFor(t, trace, "drop: dcc")

	for i := 0; i < channelBuffer; i++ {
		offer := nextOffer(t, client.DCC())
		if offer.Name != fmt.Sprintf("book%d.epub", i) {
			t.Fatalf("offer %d = %+v, want book%d.epub", i, offer, i)
		}
	}
	for i := 0; i < channelBuffer; i++ {
		nextMessage(t, client.Messages())
	}
	// The reader kept going after the drop.
	f.write(":srv PRIVMSG booxd :after\r\n")
	if msg := nextMessage(t, client.Messages()); msg.Trailing != "after" {
		t.Fatalf("message after the full DCC channel = %+v, want %q", msg, "after")
	}
	noOffer(t, client.DCC())
	if n := countTrace(trace.String(), "drop: dcc"); n != 1 {
		t.Fatalf("drop traces = %d, want 1: %q", n, trace.String())
	}
}

// waitFor waits until the trace holds substr, which proves the reader reached
// that line.
func waitFor(t *testing.T, trace *syncBuffer, substr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(trace.String(), substr) {
		if time.Now().After(deadline) {
			t.Fatalf("trace never held %q: %q", substr, trace.String())
		}
		<-time.After(5 * time.Millisecond)
	}
}

// assertOffer compares every Offer field.
func assertOffer(t *testing.T, got, want dcc.Offer) {
	t.Helper()
	if got.From != want.From || got.Name != want.Name || got.Port != want.Port ||
		got.Size != want.Size || !got.IP.Equal(want.IP) {
		t.Fatalf("offer = %+v, want %+v", got, want)
	}
}

// nextOffer waits for one offer.
func nextOffer(t *testing.T, ch <-chan dcc.Offer) dcc.Offer {
	t.Helper()
	select {
	case offer, ok := <-ch:
		if !ok {
			t.Fatal("DCC() closed early")
		}
		return offer
	case <-time.After(2 * time.Second):
		t.Fatal("no offer within 2s")
		return dcc.Offer{}
	}
}

// noOffer fails when an offer arrives shortly after.
func noOffer(t *testing.T, ch <-chan dcc.Offer) {
	t.Helper()
	select {
	case offer, ok := <-ch:
		if ok {
			t.Fatalf("unexpected offer %+v", offer)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// countTrace counts trace lines containing substr.
func countTrace(trace, substr string) int {
	n := 0
	for _, line := range strings.Split(trace, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}
