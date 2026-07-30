package infrastructure

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	BackendWorkloadURI = "spiffe://stratum.local/ns/stratum/sa/stratum-backend"
	MetricsWorkloadURI = "spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp-monitor"
)

type TLSFiles struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
}

type TLSReloader struct {
	files   TLSFiles
	current atomic.Pointer[tls.Config]
}

func NewTLSReloader(files TLSFiles) *TLSReloader {
	return &TLSReloader{files: files}
}

func (r *TLSReloader) Reload() error {
	certificate, err := tls.LoadX509KeyPair(r.files.CertFile, r.files.KeyFile)
	if err != nil {
		return fmt.Errorf("load Platform MCP certificate: %w", err)
	}
	clientCAs, err := loadCertPool(r.files.ClientCAFile)
	if err != nil {
		return err
	}
	next := &tls.Config{
		MinVersion:       tls.VersionTLS12,
		Certificates:     []tls.Certificate{certificate},
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientCAs:        clientCAs,
		VerifyConnection: verifyAllowedClientConnection,
	}
	r.current.Store(next)
	return nil
}

func (r *TLSReloader) Ready() bool {
	return r.current.Load() != nil
}

func (r *TLSReloader) Current() *tls.Config {
	return r.current.Load()
}

func (r *TLSReloader) CertificateExpirySeconds() (float64, error) {
	current := r.current.Load()
	if current == nil || len(current.Certificates) != 1 || len(current.Certificates[0].Certificate) == 0 {
		return 0, errors.New("Platform MCP certificate is not loaded")
	}
	leaf, err := x509.ParseCertificate(current.Certificates[0].Certificate[0])
	if err != nil {
		return 0, fmt.Errorf("parse Platform MCP certificate: %w", err)
	}
	return time.Until(leaf.NotAfter).Seconds(), nil
}

func (r *TLSReloader) ServerConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			current := r.current.Load()
			if current == nil {
				return nil, errors.New("Platform MCP TLS is not ready")
			}
			return current, nil
		},
	}
}

func (r *TLSReloader) BackendClientConfig(serverName string) (*tls.Config, error) {
	current := r.current.Load()
	if current == nil {
		return nil, errors.New("Platform MCP TLS is not ready")
	}
	if strings.TrimSpace(serverName) == "" {
		return nil, errors.New("Stratum backend TLS server name is required")
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		ServerName:       serverName,
		RootCAs:          current.ClientCAs.Clone(),
		Certificates:     append([]tls.Certificate(nil), current.Certificates...),
		VerifyConnection: verifyBackendConnection,
	}, nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Platform MCP client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("parse Platform MCP client CA: no certificates found")
	}
	return pool, nil
}

func verifyBackendConnection(state tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return errors.New("Stratum backend client certificate was not verified")
	}
	leaf := state.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != BackendWorkloadURI {
		return errors.New("Stratum backend SPIFFE identity denied")
	}
	return nil
}

func verifyAllowedClientConnection(state tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return errors.New("Platform MCP client certificate was not verified")
	}
	leaf := state.PeerCertificates[0]
	if len(leaf.URIs) != 1 {
		return errors.New("Platform MCP client SPIFFE identity denied")
	}
	identity := leaf.URIs[0].String()
	if identity != BackendWorkloadURI && identity != MetricsWorkloadURI {
		return errors.New("Platform MCP client SPIFFE identity denied")
	}
	return nil
}
