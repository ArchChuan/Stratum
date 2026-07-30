package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/byteBuilderX/stratum/internal/platformmcp/infrastructure"
)

const (
	defaultPlatformMCPPort       = "8443"
	defaultStratumInternalOrigin = "https://stratum-internal:8443"
	defaultStratumServerName     = "stratum-internal"
)

type runtimeConfig struct {
	port              string
	backendBaseURL    string
	backendServerName string
	tlsFiles          infrastructure.TLSFiles
}

func loadRuntimeConfig() (runtimeConfig, error) {
	cfg := runtimeConfig{
		port:              envOrDefault("PLATFORM_MCP_PORT", defaultPlatformMCPPort),
		backendBaseURL:    envOrDefault("STRATUM_INTERNAL_BASE_URL", defaultStratumInternalOrigin),
		backendServerName: envOrDefault("STRATUM_INTERNAL_SERVER_NAME", defaultStratumServerName),
		tlsFiles: infrastructure.TLSFiles{
			CertFile:     strings.TrimSpace(os.Getenv("PLATFORM_MCP_TLS_CERT_FILE")),
			KeyFile:      strings.TrimSpace(os.Getenv("PLATFORM_MCP_TLS_KEY_FILE")),
			ClientCAFile: strings.TrimSpace(os.Getenv("PLATFORM_MCP_CLIENT_CA_FILE")),
		},
	}
	if err := validateRuntimeConfig(cfg); err != nil {
		return runtimeConfig{}, err
	}
	return cfg, nil
}

func (c runtimeConfig) listenAddress() string {
	return net.JoinHostPort("", c.port)
}

func validateRuntimeConfig(cfg runtimeConfig) error {
	if cfg.tlsFiles.CertFile == "" || cfg.tlsFiles.KeyFile == "" || cfg.tlsFiles.ClientCAFile == "" {
		return errors.New("Platform MCP TLS certificate, key, and client CA files are required")
	}
	port, err := strconv.Atoi(cfg.port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("Platform MCP port is invalid: %q", cfg.port)
	}
	if strings.TrimSpace(cfg.backendBaseURL) == "" || strings.TrimSpace(cfg.backendServerName) == "" {
		return errors.New("Stratum internal endpoint and TLS server name are required")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
