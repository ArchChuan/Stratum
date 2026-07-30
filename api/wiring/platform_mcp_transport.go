package wiring

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/config"
)

const platformMCPServerName = "stratum-platform-mcp"

type platformMCPTransportProvider struct {
	files config.InternalAPIConfig
}

func (p platformMCPTransportProvider) Transport() (http.RoundTripper, error) {
	certificate, err := tls.LoadX509KeyPair(p.files.CertFile, p.files.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Stratum backend workload certificate: %w", err)
	}
	roots, err := platformMCPRootCAs(p.files.ClientCAFile)
	if err != nil {
		return nil, err
	}
	return &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, ServerName: platformMCPServerName,
		RootCAs: roots, Certificates: []tls.Certificate{certificate},
		VerifyConnection: verifyPlatformMCPServer,
	}}, nil
}

func platformMCPRootCAs(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Platform MCP server CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, errors.New("parse Platform MCP server CA: no certificates found")
	}
	return pool, nil
}

func verifyPlatformMCPServer(state tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return errors.New("Platform MCP server certificate was not verified")
	}
	leaf := state.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != middleware.PlatformMCPWorkloadURI {
		return errors.New("Platform MCP server SPIFFE identity denied")
	}
	return nil
}
