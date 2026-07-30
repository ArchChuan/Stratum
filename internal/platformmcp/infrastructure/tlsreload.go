package infrastructure

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

const BackendWorkloadURI = "spiffe://stratum.local/ns/stratum/sa/stratum-backend"

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
		VerifyConnection: verifyBackendConnection,
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
