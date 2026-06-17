package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
	maxIdleConns          = 100
	idleConnTimeout       = 90 * time.Second
)

func newTransport(c *config) http.RoundTripper {
	base := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
	}
	if c.ssrfSafe {
		base.DialContext = ssrfSafeDial(dialTimeout)
	}
	if c.userAgent == "" {
		return base
	}
	return &uaTransport{base: base, ua: c.userAgent}
}

type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" && t.ua != "" {
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// isPrivateIP reports whether ip is loopback, RFC1918 private,
// link-local (uni- or multicast), multicast, or unspecified.
// These ranges are blocked by the SSRF-safe dialer.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

func ssrfSafeDial(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("httpclient: invalid address %q: %w", addr, err)
		}
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("httpclient: DNS lookup %q: %w", host, err)
		}
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && isPrivateIP(ip) {
				return nil, fmt.Errorf("httpclient: SSRF protection blocked private address %s", a)
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0], port))
	}
}
