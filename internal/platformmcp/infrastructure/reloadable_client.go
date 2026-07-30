package infrastructure

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

type ReloadableBackendClient struct {
	tls        *TLSReloader
	serverName string
	current    atomic.Pointer[http.Client]
}

func NewReloadableBackendClient(
	tlsReloader *TLSReloader,
	serverName string,
) (*ReloadableBackendClient, error) {
	if tlsReloader == nil {
		return nil, errors.New("Platform MCP TLS reloader is not configured")
	}
	client := &ReloadableBackendClient{tls: tlsReloader, serverName: serverName}
	if err := client.Reload(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *ReloadableBackendClient) Do(req *http.Request) (*http.Response, error) {
	client := c.current.Load()
	if client == nil {
		return nil, errors.New("Stratum backend HTTP client is not ready")
	}
	// #nosec G704 -- callers accept only constructor-validated fixed Stratum internal URLs.
	return client.Do(req)
}

func (c *ReloadableBackendClient) Reload() error {
	tlsConfig, err := c.tls.BackendClientConfig(c.serverName)
	if err != nil {
		return fmt.Errorf("build Stratum backend TLS transport: %w", err)
	}
	next := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   constants.SystemAssistantToolTimeout,
	}
	previous := c.current.Swap(next)
	if previous != nil {
		previous.CloseIdleConnections()
	}
	return nil
}

func (c *ReloadableBackendClient) Close() {
	client := c.current.Swap(nil)
	if client != nil {
		client.CloseIdleConnections()
	}
}
