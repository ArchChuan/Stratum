package infrastructure

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TestValidateMCPURL 覆盖 URL 级 SSRF 检查：scheme 白名单、userinfo 拒绝、
// 空 host 拒绝。
func TestValidateMCPURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https ok", raw: "https://example.com/mcp"},
		{name: "http ok", raw: "http://example.com:8080/mcp"},
		{name: "userinfo rejected", raw: "https://user:pass@example.com/mcp", wantErr: true},
		{name: "user only rejected", raw: "https://user@example.com/mcp", wantErr: true},
		{name: "ftp rejected", raw: "ftp://example.com/mcp", wantErr: true},
		{name: "file rejected", raw: "file:///etc/passwd", wantErr: true},
		{name: "empty host rejected", raw: "https:///mcp", wantErr: true},
		{name: "empty url rejected", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ValidateMCPURL(tc.raw)
			if tc.wantErr {
				require.ErrorIs(t, err, mcpdomain.ErrInvalidServerURL)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, u)
		})
	}
}

// TestValidateIP 表驱动覆盖全部拒绝前缀：RFC1918、loopback、link-local、
// CGNAT、云元数据、文档网段、multicast、保留段；block=false 条目在
// AllowPrivate 下也必须仍被放行（策略只放宽、不收紧）。
func TestValidateIP(t *testing.T) {
	cases := []struct {
		name  string
		addr  string
		block bool
	}{
		// IPv4 拒绝段
		{name: "loopback", addr: "127.0.0.1", block: true},
		{name: "rfc1918 10", addr: "10.0.0.1", block: true},
		{name: "rfc1918 172", addr: "172.16.0.1", block: true},
		{name: "rfc1918 192.168", addr: "192.168.1.1", block: true},
		{name: "link-local", addr: "169.254.169.254", block: true},
		{name: "cg nat", addr: "100.64.0.1", block: true},
		{name: "this network", addr: "0.0.0.0", block: true},
		{name: "multicast", addr: "224.0.0.1", block: true},
		{name: "reserved", addr: "240.0.0.1", block: true},
		{name: "doc net 1", addr: "192.0.2.1", block: true},
		{name: "doc net 2", addr: "198.51.100.1", block: true},
		{name: "doc net 3", addr: "203.0.113.1", block: true},
		{name: "benchmark", addr: "198.18.0.1", block: true},
		// IPv6 拒绝段
		{name: "ipv6 loopback", addr: "::1", block: true},
		{name: "unique-local", addr: "fc00::1", block: true},
		{name: "ipv6 link-local", addr: "fe80::1", block: true},
		{name: "ipv6 doc", addr: "2001:db8::1", block: true},
		{name: "teredo", addr: "2001::1", block: true},
		{name: "6to4", addr: "2002::1", block: true},
		{name: "ipv6 multicast", addr: "ff00::1", block: true},
		// 公网放行
		{name: "public ipv4", addr: "8.8.8.8", block: false},
		{name: "public ipv6", addr: "2606:4700:4700::1111", block: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tc.addr)
			got := ValidateIP(addr, URLPolicyStrict)
			if tc.block {
				require.False(t, got, "expected %s blocked under strict", tc.addr)
				require.True(t, ValidateIP(addr, URLPolicyAllowPrivate), "expected %s allowed under AllowPrivate", tc.addr)
			} else {
				require.True(t, got, "expected %s allowed under strict", tc.addr)
			}
		})
	}
}

// TestValidateIPUnmapsMappedAddresses 验证 IPv4-mapped IPv6 地址
// （::ffff:a.b.c.d）先 Unmap 再查 v4 表，防止绕过。
func TestValidateIPUnmapsMappedAddresses(t *testing.T) {
	addr := netip.MustParseAddr("::ffff:127.0.0.1")
	require.True(t, addr.Is4In6())
	require.False(t, ValidateIP(addr, URLPolicyStrict), "mapped loopback must be blocked")
}

// TestSameOrigin 覆盖默认端口折叠、host 大小写、scheme/host/port 变化。
func TestSameOrigin(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return u
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "same", a: "https://example.com/mcp", b: "https://example.com/other", want: true},
		{name: "default port folded", a: "https://example.com/mcp", b: "https://example.com:443/mcp", want: true},
		{name: "explicit 80 folded", a: "http://example.com:80/", b: "http://example.com/", want: true},
		{name: "host case insensitive", a: "https://Example.COM/mcp", b: "https://example.com/", want: true},
		{name: "different scheme", a: "https://example.com/", b: "http://example.com/", want: false},
		{name: "different host", a: "https://example.com/", b: "https://evil.example.com/", want: false},
		{name: "different port", a: "https://example.com:8080/", b: "https://example.com:9090/", want: false},
		{name: "port vs folded", a: "https://example.com/", b: "https://example.com:8443/", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sameOrigin(parse(tc.a), parse(tc.b)))
		})
	}
}

// TestMCPCheckRedirect 覆盖跨源重定向拒绝、POST→GET 方法变更拒绝、
// Location 带 userinfo 拒绝、同源 307/308 放行。
func TestMCPCheckRedirect(t *testing.T) {
	req := func(method, raw string) *http.Request {
		t.Helper()
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return &http.Request{Method: method, URL: u}
	}
	cases := []struct {
		name string
		via  []*http.Request // 已发出的请求列表（Go CheckRedirect 语义）
		req  *http.Request   // 待跟随的目标请求
		want error
	}{
		{name: "first request allowed", via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")}, req: req(http.MethodPost, "https://example.com/mcp")},
		{name: "same origin 307 allowed",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodPost, "https://example.com/mcp/redirect")},
		{name: "cross host rejected",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodPost, "https://evil.example.com/mcp"), want: mcpdomain.ErrInvalidServerURL},
		{name: "cross port rejected",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodPost, "https://example.com:8443/mcp"), want: mcpdomain.ErrInvalidServerURL},
		{name: "cross scheme rejected",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodPost, "http://example.com/mcp"), want: mcpdomain.ErrInvalidServerURL},
		{name: "post to get rejected",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodGet, "https://example.com/mcp/redirect"), want: mcpdomain.ErrInvalidServerURL},
		{name: "location with userinfo rejected",
			via: []*http.Request{req(http.MethodPost, "https://example.com/mcp")},
			req: req(http.MethodPost, "https://user:pass@example.com/mcp/redirect"), want: mcpdomain.ErrInvalidServerURL},
		{name: "get redirect allowed",
			via: []*http.Request{req(http.MethodGet, "https://example.com/mcp")},
			req: req(http.MethodGet, "https://example.com/mcp/redirect")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want != nil {
				require.ErrorIs(t, mcpCheckRedirect(tc.req, tc.via), tc.want)
				return
			}
			require.NoError(t, mcpCheckRedirect(tc.req, tc.via))
		})
	}
}

// roundTripperFunc adapts a function to http.RoundTripper (the standard
// library has no such adapter).
type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type recordingDialer struct {
	dialed []string
	conn   net.Conn
	err    error
}

func (d *recordingDialer) DialContext(_ context.Context, _ string, addr string) (net.Conn, error) {
	d.dialed = append(d.dialed, addr)
	return d.conn, d.err
}

// setLookupMCPIPsForTest swaps the package resolver under its mutex. dialMCP
// reads it via resolveMCPIPs, which snapshots under the same lock, so parallel
// tests dialing through the real http transport must not race the swap.
func setLookupMCPIPsForTest(fn func(context.Context, string) ([]netip.Addr, error)) {
	lookupMCPIPsMu.Lock()
	lookupMCPIPs = fn
	lookupMCPIPsMu.Unlock()
}

// TestDialMCPRebinding 模拟 DNS rebinding：hostname 解析出私网 IP 时全部
// 拒绝，且不存在以 hostname 为拨号地址的回退（有回退则 loopback 解析会
// 直接连上）。解析同时含公网和私网 IP 时只拨公网候选。
func TestDialMCPRebinding(t *testing.T) {
	lookupMCPIPsMu.RLock()
	orig := lookupMCPIPs
	lookupMCPIPsMu.RUnlock()
	t.Cleanup(func() { setLookupMCPIPsForTest(orig) })

	t.Run("all private rejected without hostname fallback", func(t *testing.T) {
		// 解析指向 loopback：如果 dialMCP 存在 hostname 回退，
		// 会尝试拨 rebind.test 而不是全拒绝。
		setLookupMCPIPsForTest(func(_ context.Context, host string) ([]netip.Addr, error) {
			require.Equal(t, "rebind.test", host)
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		})
		dialer := &recordingDialer{conn: &net.TCPConn{}}
		_, err := dialMCP(context.Background(), "tcp", "rebind.test:8080", dialer, URLPolicyStrict)
		require.Error(t, err)
		require.Contains(t, err.Error(), "blocked by SSRF policy")
		require.Empty(t, dialer.dialed, "no address may be dialed when every candidate is blocked")
	})

	t.Run("mixed resolution dials only public candidates", func(t *testing.T) {
		// rebinding 混合解析：一个公网 + 一个私网，只拨公网。
		setLookupMCPIPsForTest(func(_ context.Context, host string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("169.254.169.254"),
			}, nil
		})
		dialer := &recordingDialer{conn: &net.TCPConn{}, err: errors.New("unreachable")}
		_, err := dialMCP(context.Background(), "tcp", "rebind.test:8080", dialer, URLPolicyStrict)
		require.Error(t, err)
		require.Equal(t, []string{"8.8.8.8:8080"}, dialer.dialed, "metadata address must never be dialed")
	})

	t.Run("allow private policy permits loopback", func(t *testing.T) {
		setLookupMCPIPsForTest(func(_ context.Context, host string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		})
		dialer := &recordingDialer{conn: &net.TCPConn{}}
		conn, err := dialMCP(context.Background(), "tcp", "rebind.test:8080", dialer, URLPolicyAllowPrivate)
		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Equal(t, []string{"127.0.0.1:8080"}, dialer.dialed)
	})
}

// TestSecureRoundTripperCredentialGating 验证凭据只注入同源请求：
// 跨源不注入、SDK 协议键被跳过、Bearer/api_key 两种模式、otel 注入。
func TestSecureRoundTripperCredentialGating(t *testing.T) {
	var captured http.Header
	next := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		captured = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`)),
			Request:    req,
		}, nil
	})

	t.Run("bearer injected on same origin", func(t *testing.T) {
		origin, err := url.Parse("https://mcp.example.com/mcp")
		require.NoError(t, err)
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{
			Headers: map[string]string{"X-Custom": "v"},
			Auth:    &MCPAuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "secret-token"},
		}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, "Bearer secret-token", captured.Get("Authorization"))
		require.Equal(t, "v", captured.Get("X-Custom"))
	})

	t.Run("api key injected on same origin", func(t *testing.T) {
		origin, err := url.Parse("https://mcp.example.com/mcp")
		require.NoError(t, err)
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{
			Auth: &MCPAuthConfig{Type: mcpdomain.AuthTypeAPIKey, APIKeyHeader: "X-API-Key", APIKeyValue: "ak"},
		}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, "ak", captured.Get("X-API-Key"))
	})

	t.Run("cross origin never injects credentials", func(t *testing.T) {
		origin, err := url.Parse("https://mcp.example.com/mcp")
		require.NoError(t, err)
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{
			Headers: map[string]string{"Authorization": "Bearer static", "X-API-Key": "ak"},
			Auth:    &MCPAuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "secret-token"},
		}, next: next}
		// 攻击者控制的同 host 不同端口（CheckRedirect 拒绝后的纵深防线）。
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com:8443/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Empty(t, captured.Get("Authorization"), "credentials must not reach a different origin")
		require.Empty(t, captured.Get("X-API-Key"))
	})

	t.Run("sdk protocol headers cannot be spoofed", func(t *testing.T) {
		origin, err := url.Parse("https://mcp.example.com/mcp")
		require.NoError(t, err)
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{
			Headers: map[string]string{
				"Content-Type":         "text/plain", // 尝试覆盖 SDK 协议键
				"Mcp-Session-Id":       "forged",
				"MCP-Protocol-Version": "9999-01-01",
				"X-Legit":              "ok",
			},
		}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Empty(t, captured.Get("Content-Type"), "SDK protocol headers must not be overridden")
		require.Empty(t, captured.Get("Mcp-Session-Id"))
		require.Empty(t, captured.Get("MCP-Protocol-Version"))
		require.Equal(t, "ok", captured.Get("X-Legit"))
	})

	t.Run("otel headers injected", func(t *testing.T) {
		// 默认全局 propagator 是 no-op；显式设置 TraceContext 再恢复。
		prev := otel.GetTextMapPropagator()
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))
		t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

		origin, err := url.Parse("https://mcp.example.com/mcp")
		require.NoError(t, err)
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		req = req.WithContext(trace.ContextWithRemoteSpanContext(context.Background(),
			trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    trace.TraceID{0x01},
				SpanID:     trace.SpanID{0x02},
				TraceFlags: trace.FlagsSampled,
			})))
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.NotEmpty(t, captured.Get("Traceparent"), "otel traceparent must be injected")
	})
}

// TestSecureRoundTripperBodyBounds 验证响应体按内容类型分流：
// JSON 被 16MB 总上限截断（MaxBytesError），SSE 长流不被总上限截断、
// 只按单行限长。
func TestSecureRoundTripperBodyBounds(t *testing.T) {
	origin, err := url.Parse("https://mcp.example.com/mcp")
	require.NoError(t, err)

	t.Run("json over 16MB errors with MaxBytesError", func(t *testing.T) {
		next := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", constants.MCPHTTPMaxResponseBytes+1))),
				Request:    req,
			}, nil
		})
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		var maxErr *http.MaxBytesError
		require.ErrorAs(t, err, &maxErr, "JSON body must be bounded by MaxBytesReader")
	})

	t.Run("sse long stream not capped by total limit", func(t *testing.T) {
		// 多帧长流（远超 16MB 总长）：单行都不超限则整流可读完。
		line := strings.Repeat("d", 1024)
		var sb strings.Builder
		for range 20 * 1024 {
			sb.WriteString("data: ")
			sb.WriteString(line)
			sb.WriteString("\n\n")
		}
		next := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sb.String())),
				Request:    req,
			}, nil
		})
		rt := &secureRoundTripper{origin: origin, cfg: &MCPServerConfig{}, next: next}
		req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "SSE stream must not hit a total byte cap")
		require.Equal(t, sb.String(), string(got))
	})
}

// TestLineLimitedReaderBoundedByLine 验证单行超限报错、短行放行、多行长流
// 不被总上限截断（SSE 语义：只按行限长）。
func TestLineLimitedReaderBoundedByLine(t *testing.T) {
	t.Run("single oversized line errors", func(t *testing.T) {
		r := &lineLimitedReader{
			r:       strings.NewReader(strings.Repeat("x", constants.MCPSSEFrameMaxBytes+1)),
			maxLine: constants.MCPSSEFrameMaxBytes,
		}
		_, err := io.ReadAll(r)
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds maximum line length")
	})

	t.Run("multiple long lines within limit pass", func(t *testing.T) {
		// 每行恰在 limit 内：多个长行累积但不超过单行限制（无总上限）。
		line := strings.Repeat("x", 1024)
		var sb strings.Builder
		for range 64 {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		r := &lineLimitedReader{r: strings.NewReader(sb.String()), maxLine: 2048}
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Len(t, got, 64*(1024+1))
	})

	t.Run("short lines pass", func(t *testing.T) {
		r := &lineLimitedReader{r: strings.NewReader("data: ok\n\ndata: done\n\n"), maxLine: 1024}
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "data: ok\n\ndata: done\n\n", string(got))
	})
}

// TestIsSSEBody 验证 SSE 响应识别：202 或 text/event-stream 都按流处理。
func TestIsSSEBody(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		want        bool
	}{
		{"202 treated as stream", http.StatusAccepted, "", true},
		{"event-stream content type", http.StatusOK, "text/event-stream", true},
		{"json is not stream", http.StatusOK, "application/json", false},
		{"empty content type is not stream", http.StatusOK, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status,
				Header: http.Header{"Content-Type": []string{tc.contentType}},
				Body:   io.NopCloser(strings.NewReader(""))}
			defer resp.Body.Close()
			require.Equal(t, tc.want, isSSEBody(resp))
		})
	}
}

// TestSecureHTTPClientBlockedBySSRF 端到端验证：生产 strict 策略连
// httptest loopback server 必须失败（URL 合法但 IP 被拒），AllowPrivate
// 策略则成功——两层策略都从 URL 校验 + 拨号校验全链通过。
func TestSecureHTTPClientBlockedBySSRF(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	origin, err := ValidateMCPURL(ts.URL)
	require.NoError(t, err)

	t.Run("strict rejects loopback", func(t *testing.T) {
		client := newSecureHTTPClient(origin, &MCPServerConfig{}, URLPolicyStrict)
		resp, err := client.Get(ts.URL)
		if resp != nil {
			resp.Body.Close()
		}
		require.Error(t, err)
	})

	t.Run("allow private reaches loopback", func(t *testing.T) {
		client := newSecureHTTPClient(origin, &MCPServerConfig{}, URLPolicyAllowPrivate)
		resp, err := client.Get(ts.URL)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	})
}
