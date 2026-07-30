package wiring

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	mcpinfra "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

func TestVerifyPlatformMCPServerRequiresExactWorkloadIdentity(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "platform MCP", uri: "spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp"},
		{name: "copycat", uri: "spiffe://stratum.local/ns/stratum/sa/copycat", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := url.Parse(tc.uri)
			if err != nil {
				t.Fatal(err)
			}
			certificate := &x509.Certificate{URIs: []*url.URL{identity}}
			state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate},
				VerifiedChains: [][]*x509.Certificate{{certificate}}}
			if gotErr := verifyPlatformMCPServer(state); (gotErr != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", gotErr, tc.wantErr)
			}
		})
	}
}

func TestBuildPlatformMCPFailsClosedWhenEnabledDependenciesAreMissing(t *testing.T) {
	container := &Container{
		Config: &config.Config{
			InternalAPI: config.InternalAPIConfig{
				CertFile: "server.crt", KeyFile: "server.key", ClientCAFile: "ca.crt",
			},
		},
	}

	err := container.buildPlatformMCP(t.Context())

	if err == nil {
		t.Fatal("expected enabled Platform MCP wiring with missing dependencies to fail")
	}
}

func TestBuildPlatformMCPSkipsDisabledInternalAPI(t *testing.T) {
	container := &Container{Config: &config.Config{}}

	if err := container.buildPlatformMCP(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildMCPConfiguresManagedTransportBeforeRestoringConnections(t *testing.T) {
	container := &Container{
		Config: &config.Config{InternalAPI: config.InternalAPIConfig{
			CertFile: "missing-server.crt", KeyFile: "missing-server.key", ClientCAFile: "missing-ca.crt",
		}},
		Logger: zap.NewNop(),
	}
	if err := container.buildMCP(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.MCP.Manager.Stop(t.Context()) })
	ctx := tenantdb.WithTenant(t.Context(), &tenantdb.TenantContext{TenantID: "tenant-1"})

	err := container.MCP.Manager.Connect(ctx, &mcpinfra.MCPServerConfig{
		ID: platformmcp.SystemServerID, Transport: "streamable-http", URL: "https://stratum-platform-mcp:8443/mcp",
		SystemKey: platformmcp.SystemServerKey, ManagementMode: platformmcp.ManagementPlatform,
	})

	if err == nil || !strings.Contains(err.Error(), "load Stratum backend workload certificate") {
		t.Fatalf("error=%v, want configured managed transport failure", err)
	}
}

func TestPlatformMCPBindingAdapterValidatesManagedResourcesAndToolLink(t *testing.T) {
	adapter := platformMCPBindingAdapter{
		agents: platformMCPAgentFake{agent: agentapp.AgentDTO{
			ID: platformmcp.SystemAssistantID, SystemKey: platformmcp.SystemAssistantKey,
			MCPToolIDs: []string{"mcp:stratum-platform-mcp:stratum_diagnose_tenant"},
		}},
		servers: platformMCPServerFake{server: &mcpdomain.ServerConfig{
			ID: platformmcp.SystemServerID, SystemKey: platformmcp.SystemServerKey,
			ManagementMode: platformmcp.ManagementPlatform,
		}},
	}

	binding, err := adapter.ReadPlatformMCPBinding(
		t.Context(), "tenant-1", platformmcp.SystemAssistantID, platformmcp.SystemServerID,
		"stratum_diagnose_tenant",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Bound || binding.AgentSystemKey != platformmcp.SystemAssistantKey ||
		binding.ServerSystemKey != platformmcp.SystemServerKey {
		t.Fatalf("binding=%+v", binding)
	}
}

func TestPlatformMCPRoleAdapterPreservesMembershipFailure(t *testing.T) {
	wantErr := context.Canceled
	adapter := platformMCPRoleAdapter{members: platformMCPMemberFake{err: wantErr}}

	_, err := adapter.CurrentRole(t.Context(), "tenant-1", "user-1")

	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v, want %v", err, wantErr)
	}
}

func TestPhase1PlatformMCPContractsAreClosed(t *testing.T) {
	registry := platformmcp.NewPhase1Contracts()
	for _, name := range platformmcp.Phase1ToolNames {
		contract, ok := registry.Lookup(name)
		if !ok || contract.Name != name || contract.Method == "" || contract.Path == "" || contract.Risk == "" {
			t.Fatalf("contract %q=%+v, found=%v", name, contract, ok)
		}
	}
	if _, ok := registry.Lookup("tenant_supplied_tool"); ok {
		t.Fatal("unexpected tenant-supplied tool contract")
	}
}

type platformMCPAgentFake struct {
	agent agentapp.AgentDTO
	err   error
}

func (f platformMCPAgentFake) Get(context.Context, string) (agentapp.AgentDTO, error) {
	return f.agent, f.err
}

type platformMCPServerFake struct {
	server *mcpdomain.ServerConfig
	err    error
}

func (f platformMCPServerFake) GetServerConfig(context.Context, string) (*mcpdomain.ServerConfig, error) {
	return f.server, f.err
}

type platformMCPMemberFake struct {
	role string
	err  error
}

func (f platformMCPMemberFake) GetMemberRole(context.Context, string, string) (string, error) {
	return f.role, f.err
}

var (
	_ iamport.PlatformMCPBindingReader = platformMCPBindingAdapter{}
	_ iamport.TenantRoleResolver       = platformMCPRoleAdapter{}
)
