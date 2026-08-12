package infrastructure

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 凭据注入断言落在 secureRoundTripper 而不是 BaseClient：旧测试直接操作已
// 删除的 client.httpClient/negotiatedVersion 字段把 Header 塞进 HTTP client，
// 而新实现把凭据注入放在 round tripper 层（SDK 的 StreamableClientTransport
// 只提供 HTTPClient 而无按请求加头回调，见 newSecureHTTPClient）。因此测试
// 直接构造 secureRoundTripper 验证三层契约：配置头注入、SDK 协议键跳过、
// 跨源不注入。

// recordingRoundTripper captures the headers a request carried once it
// reached the transport.
type recordingRoundTripper struct {
	headers []http.Header
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.headers = append(r.headers, req.Header.Clone())
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, nil
}

func TestSecureRoundTripperInjectsConfiguredAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)

	origin, err := url.Parse(server.URL)
	require.NoError(t, err)
	recorded := &recordingRoundTripper{}
	rt := &secureRoundTripper{
		origin: origin,
		cfg: &MCPServerConfig{
			Headers: map[string]string{"Authorization": "Bearer static"},
		},
		next: recorded,
	}

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, "Bearer static", recorded.headers[0].Get("Authorization"))
}

// TestSecureRoundTripperSkipsSDKProtocolHeaders verifies that tenant-configured
// headers colliding with protocol state (content-type, accept,
// mcp-protocol-version, mcp-session-id, last-event-id) are not injected,
// case-insensitively: a tenant must not be able to spoof the negotiated
// protocol version or session id the SDK transport owns on every request.
func TestSecureRoundTripperSkipsSDKProtocolHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)

	origin, err := url.Parse(server.URL)
	require.NoError(t, err)
	recorded := &recordingRoundTripper{}
	rt := &secureRoundTripper{
		origin: origin,
		cfg: &MCPServerConfig{
			Headers: map[string]string{
				"Authorization":        "Bearer static",
				"content-type":         "application/json",  // SDK-owned, 小写
				"ACCEPT":               "text/event-stream", // SDK-owned, 大写
				"mcp-protocol-version": "2025-06-18",        // SDK-owned
				"Mcp-Session-Id":       "spoofed",           // SDK-owned
				"last-event-id":        "spoofed",           // SDK-owned
				"X-Custom":             "kept",
			},
		},
		next: recorded,
	}

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()

	h := recorded.headers[0]
	require.Equal(t, "Bearer static", h.Get("Authorization"))
	require.Equal(t, "", h.Get("Content-Type"), "SDK-owned content-type must not be tenant-overridden")
	require.Equal(t, "", h.Get("Accept"), "SDK-owned accept must not be tenant-overridden")
	require.Equal(t, "", h.Get("Mcp-Protocol-Version"), "SDK-owned protocol version must not be tenant-overridden")
	require.Equal(t, "", h.Get("Mcp-Session-Id"), "SDK-owned session id must not be tenant-overridden")
	require.Equal(t, "", h.Get("Last-Event-Id"), "SDK-owned last-event-id must not be tenant-overridden")
	require.Equal(t, "kept", h.Get("X-Custom"))
}

// TestSecureRoundTripperSkipsCrossOriginRequests verifies that credentials are
// injected only when the request still targets the connection's origin
// snapshot: a cross-origin request (different host/port) must never carry the
// configured credentials or custom headers. This is defense in depth on top
// of CheckRedirect, because http.Client copies the initial request's headers
// into redirect hops.
func TestSecureRoundTripperSkipsCrossOriginRequests(t *testing.T) {
	originServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(originServer.Close)
	targetServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(targetServer.Close)
	require.NotEqual(t, originServer.URL, targetServer.URL, "two httptest servers must differ in port")

	origin, err := url.Parse(originServer.URL)
	require.NoError(t, err)
	recorded := &recordingRoundTripper{}
	rt := &secureRoundTripper{
		origin: origin,
		cfg: &MCPServerConfig{
			Headers: map[string]string{"Authorization": "Bearer static", "X-Custom": "secret"},
		},
		next: recorded,
	}

	req, err := http.NewRequest(http.MethodPost, targetServer.URL, nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()

	h := recorded.headers[0]
	require.Equal(t, "", h.Get("Authorization"), "cross-origin request must not carry credentials")
	require.Equal(t, "", h.Get("X-Custom"), "cross-origin request must not carry custom headers")
	require.True(t, strings.HasPrefix(targetServer.URL, "http://"), "targetServer.URL scheme sanity")
}
