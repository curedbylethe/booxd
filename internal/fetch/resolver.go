package fetch

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func dnsServer(path string) string {
	body, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(fields) >= 2 && fields[0] == "nameserver" && net.ParseIP(fields[1]) != nil {
			return net.JoinHostPort(fields[1], "53")
		}
	}
	// shortcut: fixed public fallback; add configuration when another server is needed.
	return "8.8.8.8:53"
}

func dnsDialer(server string) *net.Dialer {
	return &net.Dialer{
		Timeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server)
		}},
	}
}

func androidTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil
	}
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		prefix = "/data/data/com.termux/files/usr"
	}
	transport := base.Clone()
	transport.DialContext = dnsDialer(dnsServer(filepath.Join(prefix, "etc", "resolv.conf"))).DialContext
	return transport
}
