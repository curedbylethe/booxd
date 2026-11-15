package irc

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is a line-scripted server over net.Pipe. It records every line the
// client writes and replies with reply's lines for it. No net.Listen.
type fake struct {
	t     *testing.T
	reply func(line string) []string
	cert  *tls.Certificate
	lines chan string
	mu    sync.Mutex
	conns []net.Conn
}

func newFake(t *testing.T, reply func(line string) []string) *fake {
	t.Helper()
	f := &fake{t: t, reply: reply, lines: make(chan string, 256)}
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, conn := range f.conns {
			_ = conn.Close()
		}
	})
	return f
}

// config returns a usable Config whose DialFunc is this fake.
func (f *fake) config() Config {
	return Config{
		Host:            "fake",
		Port:            6667,
		Nick:            "booxd",
		Channel:         "#ebooks",
		DialTimeout:     2 * time.Second,
		RegisterTimeout: 2 * time.Second,
		JoinTimeout:     2 * time.Second,
		SendTimeout:     2 * time.Second,
		IdleTimeout:     2 * time.Second,
		DialFunc:        f.dial,
	}
}

func (f *fake) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	client, server := net.Pipe()
	f.mu.Lock()
	f.conns = append(f.conns, server)
	f.mu.Unlock()
	go f.serve(server)
	return client, nil
}

// serve reads client lines and writes scripted replies from a second
// goroutine, so a reply never blocks the reader.
func (f *fake) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if f.cert != nil {
		conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*f.cert}})
	}
	out := make(chan string, 256)
	go func() {
		w := bufio.NewWriter(conn)
		for line := range out {
			if _, err := w.WriteString(line + "\r\n"); err != nil {
				return
			}
			if err := w.Flush(); err != nil {
				return
			}
		}
	}()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		f.lines <- line
		if f.reply == nil {
			continue
		}
		for _, response := range f.reply(line) {
			out <- response
		}
	}
	close(out)
}

// write sends raw bytes on the server side of every pipe. Tests use it for
// input the reply script cannot produce: a bare LF or an over-long line.
func (f *fake) write(raw string) {
	f.t.Helper()
	f.mu.Lock()
	conns := append([]net.Conn(nil), f.conns...)
	f.mu.Unlock()
	for _, conn := range conns {
		if _, err := io.WriteString(conn, raw); err != nil {
			f.t.Errorf("fake: write: %v", err)
		}
	}
}

// closeAll drops the server side of every pipe, as a server would.
func (f *fake) closeAll() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, conn := range f.conns {
		_ = conn.Close()
	}
}

// collect waits for n lines the client wrote.
func (f *fake) collect(n int) []string {
	f.t.Helper()
	lines := make([]string, 0, n)
	timeout := time.After(2 * time.Second)
	for len(lines) < n {
		select {
		case line := <-f.lines:
			lines = append(lines, line)
		case <-timeout:
			f.t.Fatalf("fake: got %d of %d lines: %q", len(lines), n, lines)
		}
	}
	return lines
}

// drain returns the lines already recorded, without waiting.
func (f *fake) drain() []string {
	var lines []string
	for {
		select {
		case line := <-f.lines:
			lines = append(lines, line)
		default:
			return lines
		}
	}
}

// selfSigned returns a certificate the client does not trust.
func selfSigned(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake"},
		DNSNames:     []string{"fake"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// welcome replies to USER with 001 and to JOIN with 366.
func welcome(line string) []string {
	switch {
	case strings.HasPrefix(line, "USER "):
		return []string{":srv 001 booxd :welcome"}
	case strings.HasPrefix(line, "JOIN "):
		return []string{":srv 366 booxd #ebooks :End of /NAMES"}
	}
	return nil
}
