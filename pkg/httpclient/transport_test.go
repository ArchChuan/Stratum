package httpclient_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/httpclient"
)

func TestSSRFSafeBlocksPrivateAddresses(t *testing.T) {
	cases := []struct {
		host string
	}{
		{"127.0.0.1:80"},
		{"localhost:80"},
		{"10.0.0.1:80"},
		{"192.168.0.1:80"},
		{"169.254.169.254:80"},
	}
	c := httpclient.NewSSRFSafe(httpclient.WithTimeout(2 * time.Second))
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+tc.host, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := c.Do(req)
			if err == nil {
				resp.Body.Close()
				t.Fatalf("expected error for %s, got nil", tc.host)
			}
			msg := err.Error()
			if !strings.Contains(msg, "SSRF") && !strings.Contains(msg, "private") && !strings.Contains(msg, "blocked") {
				t.Fatalf("expected SSRF/private/blocked error for %s, got: %v", tc.host, err)
			}
		})
	}
}

func TestNewSetsUserAgent(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := httpclient.New(httpclient.WithUserAgent("test-agent/1.0"))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "test-agent/1.0" {
		t.Fatalf("expected UA test-agent/1.0, got %q", got)
	}
}

func TestNewPreservesExplicitUserAgent(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := httpclient.New(httpclient.WithUserAgent("default/1.0"))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "explicit/2.0")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "explicit/2.0" {
		t.Fatalf("expected explicit UA preserved, got %q", got)
	}
}

func TestWithDisableRedirectsReturnsLastResponse(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := httpclient.New(httpclient.WithDisableRedirects())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 server hit, got %d", hits)
	}
}

func TestWithCheckRedirectOverridesDisableRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	sentinel := errors.New("redirects-not-allowed")
	c := httpclient.New(
		httpclient.WithDisableRedirects(),
		httpclient.WithCheckRedirect(func(*http.Request, []*http.Request) error {
			return sentinel
		}),
	)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.Do(req)
	if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

func TestWithTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := httpclient.New(httpclient.WithTimeout(50 * time.Millisecond))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.Do(req)
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("expected timeout error")
	}
}

// staticDoer used only to make sure Doer interface compiles against *http.Client.
var _ httpclient.Doer = (*http.Client)(nil)

// keep fmt import used in case future cases need it.
var _ = fmt.Sprint
