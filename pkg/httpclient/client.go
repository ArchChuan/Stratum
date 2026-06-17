// Package httpclient provides shared HTTP client builders with timeout,
// retry, and SSRF protection presets. Business code should use New for
// outbound calls to trusted internal/external APIs and NewSSRFSafe for
// any user-supplied URL (e.g., HTTP skills, webhooks).
package httpclient

import (
	"net/http"
	"time"
)

// Doer abstracts the http.Client.Do method so tests can substitute fakes.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Option configures a client built by New or NewSSRFSafe.
type Option func(*config)

type config struct {
	timeout          time.Duration
	userAgent        string
	ssrfSafe         bool
	disableRedirects bool
	checkRedirect    func(*http.Request, []*http.Request) error
}

// WithTimeout sets the http.Client.Timeout.
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithUserAgent sets a default User-Agent header for outbound requests.
// An explicit User-Agent on the request is preserved.
func WithUserAgent(ua string) Option { return func(c *config) { c.userAgent = ua } }

// WithDisableRedirects causes the client to return the last response
// instead of following redirects (http.ErrUseLastResponse).
func WithDisableRedirects() Option { return func(c *config) { c.disableRedirects = true } }

// WithCheckRedirect installs a custom CheckRedirect handler. It takes
// precedence over WithDisableRedirects.
func WithCheckRedirect(fn func(*http.Request, []*http.Request) error) Option {
	return func(c *config) { c.checkRedirect = fn }
}

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "stratum/1.0"
)

// New returns an HTTP client preconfigured with sensible timeouts and a
// User-Agent. Use this for outbound calls to trusted endpoints.
func New(opts ...Option) *http.Client {
	c := &config{timeout: defaultTimeout, userAgent: defaultUserAgent}
	for _, o := range opts {
		o(c)
	}
	return buildClient(c)
}

// NewSSRFSafe returns an HTTP client whose dialer rejects connections
// to loopback / private / link-local / multicast / unspecified IP
// addresses, mitigating SSRF attacks against user-supplied URLs.
func NewSSRFSafe(opts ...Option) *http.Client {
	c := &config{timeout: defaultTimeout, userAgent: defaultUserAgent, ssrfSafe: true}
	for _, o := range opts {
		o(c)
	}
	return buildClient(c)
}

func buildClient(c *config) *http.Client {
	cl := &http.Client{Timeout: c.timeout, Transport: newTransport(c)}
	switch {
	case c.checkRedirect != nil:
		cl.CheckRedirect = c.checkRedirect
	case c.disableRedirects:
		cl.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return cl
}
