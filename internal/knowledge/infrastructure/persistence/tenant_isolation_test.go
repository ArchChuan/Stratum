package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestKnowledgeRepositoriesRejectInvalidTenantBeforeUsingPool(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{name: "workspace", call: func() error { _, err := NewWorkspaceRepo(nil).List(context.Background(), `bad"tenant`); return err }},
		{name: "document", call: func() error {
			_, err := NewDocRepo(nil).CountByWorkspace(context.Background(), `bad"tenant`, "ws-1")
			return err
		}},
		{name: "chunk", call: func() error {
			_, err := NewChunkRepo(nil).KeywordSearch(context.Background(), `bad"tenant`, "ws-1", "query", 1)
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "postgres: invalid tenant_id") {
				t.Fatalf("expected validated tenant error, got %v", err)
			}
		})
	}
}
