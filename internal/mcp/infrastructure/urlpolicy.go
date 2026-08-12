package infrastructure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// URLPolicyOption selects the strictness of the outbound SSRF guard.
type URLPolicyOption int

const (
	// URLPolicyStrict (default) rejects loopback/private/link-local/metadata
	// targets. Production client construction always uses this policy.
	URLPolicyStrict URLPolicyOption = iota
	// URLPolicyAllowPrivate additionally permits loopback/private targets.
	// Only tests exercise the client against httptest servers under this
	// policy; no production call site may pass it.
	URLPolicyAllowPrivate
)

// blockedIPv4Prefixes and blockedIPv6Prefixes are the address families the
// MCP client must never dial. netip prefix matching keeps the guard a table
// lookup instead of an if-else chain.
var blockedIPv4Prefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this" network
	netip.MustParsePrefix("10.0.0.0/8"),      // RFC1918
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("127.0.0.0/8"),     // loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // link-local + cloud metadata
	netip.MustParsePrefix("172.16.0.0/12"),   // RFC1918
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1 (documentation)
	netip.MustParsePrefix("192.168.0.0/16"),  // RFC1918
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmark
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2 (documentation)
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3 (documentation)
	netip.MustParsePrefix("224.0.0.0/4"),     // multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
}

var blockedIPv6Prefixes = []netip.Prefix{
	netip.MustParsePrefix("::1/128"),       // loopback
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64 well-known prefix（内嵌 IPv4，可编码 127.0.0.1/私网/云元数据）
	netip.MustParsePrefix("fc00::/7"),      // unique-local
	netip.MustParsePrefix("fe80::/10"),     // link-local
	netip.MustParsePrefix("2001:db8::/32"), // documentation
	netip.MustParsePrefix("2001::/32"),     // Teredo
	netip.MustParsePrefix("2002::/16"),     // 6to4
	netip.MustParsePrefix("ff00::/8"),      // multicast
}

// ValidateIP reports whether addr may be dialed under the given policy.
// IPv4-mapped IPv6 addresses (::ffff:a.b.c.d) are unmapped first so the
// v4 table applies to them.
func ValidateIP(addr netip.Addr, policy URLPolicyOption) bool {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	table := blockedIPv4Prefixes
	if addr.Is6() {
		table = blockedIPv6Prefixes
	}
	for _, p := range table {
		if p.Contains(addr) {
			return policy == URLPolicyAllowPrivate
		}
	}
	return true
}

// ValidateMCPURL validates an MCP endpoint URL and returns its normalized
// form. Rejected: schemes outside http/https, empty hosts, and embedded
// userinfo. Credentials in the URL would be persisted at rest by
// persistConnect, echo into transport errors, and auto-attach as Basic auth
// alongside the bearer header; the only supported credential channel is
// AuthConfig over the round tripper.
func ValidateMCPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, mcpdomain.ErrInvalidServerURL
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, mcpdomain.ErrInvalidServerURL
	}
	if u.User != nil {
		return nil, mcpdomain.ErrInvalidServerURL
	}
	return u, nil
}

// sameOrigin reports whether a and b share scheme and host:port. Default
// ports are folded, host comparison is case-insensitive, and an explicit
// port equal to the scheme default is folded as well.
func sameOrigin(a, b *url.URL) bool {
	if a.Scheme != b.Scheme || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	ap, bp := a.Port(), b.Port()
	if ap == "" {
		ap = defaultPort(a.Scheme)
	}
	if bp == "" {
		bp = defaultPort(b.Scheme)
	}
	return ap == bp
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// mcpCheckRedirect rejects cross-origin redirects outright and POST→GET
// method changes. Go only strips the four standard sensitive headers on
// redirect; custom credential headers such as X-API-Key would be forwarded
// verbatim to the target, so a redirect to another origin is never followed.
// A 301/302/303 on a POST would silently convert to GET and drop the body,
// so that transition is rejected even when same-origin. A redirect chain is
// also bounded by MCPMaxRedirects: without it, a same-origin 307 loop would
// recurse without limit.
func mcpCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= constants.MCPMaxRedirects {
		return mcpdomain.ErrInvalidServerURL
	}
	prev := via[len(via)-1]
	if !sameOrigin(prev.URL, req.URL) || req.URL.User != nil {
		return mcpdomain.ErrInvalidServerURL
	}
	if via[0].Method == http.MethodPost && req.Method != http.MethodPost {
		return mcpdomain.ErrInvalidServerURL
	}
	return nil
}

// lookupMCPIPs resolves a host to its candidate addresses. Extracted as a
// package variable so tests can swap in a resolver that simulates DNS
// rebinding (a hostname resolving differently across calls). The mutex keeps
// parallel tests that swap the resolver race-free against concurrent dials
// from other tests; production never writes it.
var (
	lookupMCPIPsMu sync.RWMutex
	lookupMCPIPs   = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
)

// resolveMCPIPs snapshots the resolver under the lock and calls it outside
// the critical section, so a swapped test resolver can block on IO without
// stalling writers.
func resolveMCPIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	lookupMCPIPsMu.RLock()
	fn := lookupMCPIPs
	lookupMCPIPsMu.RUnlock()
	return fn(ctx, host)
}

// mcpDialer is the dial capability dialMCP needs; *net.Dialer satisfies it.
// Interface (not *net.Dialer) so tests can inject a recording dialer to
// assert which addresses were attempted.
type mcpDialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// dialMCP resolves host to all candidate IPs and dials only addresses that
// pass ValidateIP. There is deliberately no fallback to dialing the hostname
// itself: such a fallback would re-open the DNS-rebinding window. TLS
// ServerName is taken from the URL host by Go's transport independently of
// the dial address, so certificate validation still follows the configured
// hostname.
func dialMCP(ctx context.Context, network, addr string, dialer mcpDialer, policy URLPolicyOption) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveMCPIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		if !ValidateIP(ip, policy) {
			continue
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all %d resolved addresses are blocked by SSRF policy", len(ips))
	}
	return nil, lastErr
}

// newSecureHTTPClient builds the hardened HTTP client for one MCP server
// connection. The returned client is per-connection: config headers and auth
// are captured in the round tripper closure, so concurrent connections with
// different credentials never share state (no package-level cache keyed by
// URL). There is no client-level Timeout: the standalone SSE stream is a
// long-lived connection; per-hop deadlines live on the Transport.
func newSecureHTTPClient(origin *url.URL, cfg *MCPServerConfig, policy URLPolicyOption) *http.Client {
	dialer := &net.Dialer{
		Timeout:   constants.MCPDefaultDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	base := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialMCP(ctx, network, addr, dialer, policy)
		},
		// 注意：不得设置 DialTLSContext。Go 的 transport 只对返回值
		// *tls.Conn 执行 HandshakeContext；自定义 DialTLSContext 返回裸
		// TCP conn 时会静默跳过 TLS handshake，把明文发到 443。SSRF 校验
		// 只关心拨号地址，走 DialContext 即可——TLS ServerName 由 transport
		// 独立取 URL host，证书校验不受影响。
		TLSHandshakeTimeout:   constants.MCPTLSHandshakeTimeout,
		ResponseHeaderTimeout: constants.MCPResponseHeaderTimeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Transport: &secureRoundTripper{
			origin: origin,
			cfg:    cfg,
			next:   base,
		},
		CheckRedirect: mcpCheckRedirect,
	}
}

// mcpSDKProtocolHeaders are headers owned by the SDK transport on every
// request. Tenant-configured headers that collide with them are ignored
// (comparison is case-insensitive) so a tenant cannot spoof protocol state
// such as the negotiated version or session id.
var mcpSDKProtocolHeaders = map[string]struct{}{
	"content-type":         {},
	"accept":               {},
	"mcp-protocol-version": {},
	"mcp-session-id":       {},
	"last-event-id":        {},
}

// secureRoundTripper applies per-request credentials and otel headers only
// when the request still targets the connection's origin snapshot (the
// redirect gate in CheckRedirect already rejects cross-origin hops; the
// origin comparison here is defense in depth, because http.Client copies the
// initial request's headers into redirect requests). Response bodies are
// bounded by content type: SSE lines by lineLimitedReader, JSON by a total
// cap.
type secureRoundTripper struct {
	origin *url.URL
	cfg    *MCPServerConfig
	next   http.RoundTripper
}

func (rt *secureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if sameOrigin(rt.origin, req.URL) {
		rt.injectHeaders(req)
		rt.injectAuth(req)
		otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	}
	resp, err := rt.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = boundResponseBody(resp)
	return resp, nil
}

// injectHeaders applies tenant-configured headers on same-origin requests,
// skipping the SDK-owned protocol keys a tenant must not spoof.
func (rt *secureRoundTripper) injectHeaders(req *http.Request) {
	for name, value := range rt.cfg.Headers {
		if _, owned := mcpSDKProtocolHeaders[strings.ToLower(name)]; owned {
			continue
		}
		req.Header.Set(name, value)
	}
}

// injectAuth applies the AuthConfig credential. doConnect fail-closes on an
// empty APIKeyHeader; the skip here is only a defensive no-op.
func (rt *secureRoundTripper) injectAuth(req *http.Request) {
	if rt.cfg.Auth == nil {
		return
	}
	switch rt.cfg.Auth.Type {
	case mcpdomain.AuthTypeBearer:
		req.Header.Set("Authorization", "Bearer "+rt.cfg.Auth.Token)
	case mcpdomain.AuthTypeAPIKey:
		if rt.cfg.Auth.APIKeyHeader != "" {
			req.Header.Set(rt.cfg.Auth.APIKeyHeader, rt.cfg.Auth.APIKeyValue)
		}
	}
}

// boundResponseBody applies the per-kind body cap: SSE streams are bounded
// per line (a total cap would truncate legitimately long tool calls), any
// other response by total bytes.
func boundResponseBody(resp *http.Response) io.ReadCloser {
	if isSSEBody(resp) {
		return &lineLimitedReader{r: resp.Body, maxLine: constants.MCPSSEFrameMaxBytes}
	}
	return http.MaxBytesReader(nil, resp.Body, constants.MCPHTTPMaxResponseBytes)
}

// isSSEBody reports whether the response body is an SSE stream (status 202
// or text/event-stream content type). Such bodies are long-lived: a total
// byte cap would truncate legitimately long tool calls, so they are bounded
// per line instead.
func isSSEBody(resp *http.Response) bool {
	if resp.StatusCode == http.StatusAccepted {
		return true
	}
	return strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}

// errSSEFrameTooLong is the permanent error reported once a single SSE line
// exceeds maxLine. It must be returned with n==0: the SDK reads frames with
// bufio.Reader.ReadBytes('\n'), which swallows an error delivered alongside
// bytes if those bytes happen to complete the line — returning (n, err) would
// let a 16MB frame through as a success.
var errSSEFrameTooLong = errors.New("mcp sse frame exceeds maximum line length")

// lineLimitedReader bounds the length of a single SSE line. The SDK reads
// SSE frames with bufio.Reader.ReadBytes('\n'), which has no size limit of
// its own; without this guard a malicious server could stream one unbounded
// line and OOM the process. Total stream length stays unbounded.
type lineLimitedReader struct {
	r        io.Reader
	maxLine  int64
	lineTail int64 // bytes since the last newline
	err      error // set permanently once a line exceeds maxLine
}

func (l *lineLimitedReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.r.Read(p)
	if n <= 0 {
		return n, err
	}
	// 逐行检查:一次 Read 可能含多行,每遇 \n 结束一行并重置累计,所以
	// 必须遍历 p[:n] 内所有行——只查第一个 \n 会让同次 Read 中的中间行
	// 绕过上限。超限返回 (0, err):若返回 n>0,bufio.ReadBytes 会先消费
	// 这些字节,行尾 \n 被吞时错误一并被吞,16MB 帧当成功交付。
	start := 0
	for {
		nl := bytes.IndexByte(p[start:n], '\n')
		if nl < 0 {
			break
		}
		l.lineTail += int64(nl + 1)
		if l.lineTail > l.maxLine {
			l.err = errSSEFrameTooLong
			return 0, l.err
		}
		l.lineTail = 0
		start += nl + 1
	}
	l.lineTail += int64(n - start)
	if l.lineTail > l.maxLine {
		l.err = errSSEFrameTooLong
		return 0, l.err
	}
	return n, err
}

// Close satisfies io.ReadCloser (http.Response.Body); the body is closed by
// the SDK transport.
func (l *lineLimitedReader) Close() error {
	if c, ok := l.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
