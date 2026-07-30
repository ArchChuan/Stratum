package main

import "testing"

func TestLoadRuntimeConfigRequiresTLSFiles(t *testing.T) {
	t.Setenv("PLATFORM_MCP_TLS_CERT_FILE", "")
	t.Setenv("PLATFORM_MCP_TLS_KEY_FILE", "")
	t.Setenv("PLATFORM_MCP_CLIENT_CA_FILE", "")

	if _, err := loadRuntimeConfig(); err == nil {
		t.Fatal("expected missing TLS files to fail")
	}
}

func TestLoadRuntimeConfigUsesFixedInternalDefaults(t *testing.T) {
	t.Setenv("PLATFORM_MCP_TLS_CERT_FILE", "/tls/tls.crt")
	t.Setenv("PLATFORM_MCP_TLS_KEY_FILE", "/tls/tls.key")
	t.Setenv("PLATFORM_MCP_CLIENT_CA_FILE", "/tls/ca.crt")
	t.Setenv("STRATUM_INTERNAL_BASE_URL", "")
	t.Setenv("STRATUM_INTERNAL_SERVER_NAME", "")

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.backendBaseURL != "https://stratum-internal:8443" ||
		cfg.backendServerName != "stratum-internal" || cfg.port != "8443" {
		t.Fatalf("runtime config=%+v", cfg)
	}
}
