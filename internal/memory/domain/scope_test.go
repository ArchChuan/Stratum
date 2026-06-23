package domain_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestValidateScope(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"user", true},
		{"agent", true},
		{"off", false},
		{"global", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := domain.ValidateScope(tt.input)
			if tt.valid && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tt.valid && err == nil {
				t.Errorf("expected invalid, got nil error")
			}
		})
	}
}

func TestBuildScopeFilter(t *testing.T) {
	userFilter := domain.BuildScopeFilter("tenant1", "user123", "agent456", "user")
	if userFilter.UserID != "user123" {
		t.Errorf("expected userID user123")
	}
	if !userFilter.IncludeUserScope {
		t.Error("expected user scope included")
	}
	if !userFilter.IncludeAgentScope {
		t.Error("expected agent scope included for read_scope=user")
	}

	agentFilter := domain.BuildScopeFilter("tenant1", "user123", "agent456", "agent")
	if agentFilter.IncludeUserScope {
		t.Error("expected user scope excluded for read_scope=agent")
	}
	if !agentFilter.IncludeAgentScope {
		t.Error("expected agent scope included")
	}
	if agentFilter.AgentID != "agent456" {
		t.Error("expected agentID set")
	}
}
