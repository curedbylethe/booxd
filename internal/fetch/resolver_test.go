package fetch

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDNSServer(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"one", "nameserver 8.8.4.4", "8.8.4.4:53"},
		{"first", "nameserver 8.8.8.8\nnameserver 8.8.4.4", "8.8.8.8:53"},
		{"ipv6", "nameserver 2001:4860:4860::8888", "[2001:4860:4860::8888]:53"},
		{"comments", "\n# nameserver 1.1.1.1\n nameserver 8.8.4.4 # comment", "8.8.4.4:53"},
		{"invalid", "nameserver hostname\nnameserver\nnameserver 8.8.4.4", "8.8.4.4:53"},
		{"empty", "", "8.8.8.8:53"},
		{"missing", "", "8.8.8.8:53"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resolv.conf")
			if tc.name != "missing" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := dnsServer(path); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDNSDialIgnoresDefaultAddress(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	dialer := dnsDialer(listener.LocalAddr().String())
	connection, err := dialer.Resolver.Dial(context.Background(), "udp", "[::1]:53")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if connection.RemoteAddr().String() != listener.LocalAddr().String() {
		t.Fatalf("remote=%s", connection.RemoteAddr())
	}
}

func TestAndroidTransportOptionsPreserveDialer(t *testing.T) {
	prefix := t.TempDir()
	t.Setenv("PREFIX", prefix)
	if err := os.Mkdir(filepath.Join(prefix, "etc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, "etc", "resolv.conf"), []byte("nameserver 8.8.4.4"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := androidTransport()
	if transport == http.DefaultTransport {
		t.Fatal("default transport modified")
	}
	dial := transport.DialContext
	calls := 0
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		calls++
		return dial(ctx, network, address)
	}
	client := New(func(c *Client) { c.http.Transport = transport })
	proxy, _ := url.Parse("http://proxy.test:8080")
	WithProxy(proxy)(client)
	WithTimeout(0)(client)
	got := client.http.Transport.(*http.Transport)
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	connection, err := got.DialContext(context.Background(), "udp", listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if calls != 1 || got.ResponseHeaderTimeout != DefaultTimeout {
		t.Fatal("options lost dialer or timeout")
	}
	configured, err := got.Proxy(&http.Request{})
	if err != nil || configured.String() != proxy.String() {
		t.Fatalf("proxy=%v error=%v", configured, err)
	}
}

type resolverTransportDouble struct{}

func (*resolverTransportDouble) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func TestAndroidTransportKeepsDefaultDouble(t *testing.T) {
	original := http.DefaultTransport
	double := &resolverTransportDouble{}
	http.DefaultTransport = double
	t.Cleanup(func() { http.DefaultTransport = original })
	if got := androidTransport(); got != nil {
		t.Fatalf("transport=%v, want nil", got)
	}
	if got := New().http.Transport; got != double {
		t.Fatalf("transport=%T, want original double", got)
	}
}
