package application

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

func TestNewKnowledgeIngest(t *testing.T) {
	logger := zap.NewNop()
	ingest := NewKnowledgeIngest(nil, nil, nil, nil, nil, logger)

	if ingest == nil {
		t.Error("expected KnowledgeIngest to be non-nil")
	}
}

func TestNewRAGService(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, nil, logger)

	if service == nil {
		t.Error("expected RAGService to be non-nil")
	}
}

func TestGetWorkspaceCollections(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name       string
		names      []string
		namesErr   error
		wantNames  []string
		wantErr    bool
		wantNonNil bool
	}{
		{
			name:      "success: returns workspace names",
			names:     []string{"ws1", "ws2"},
			wantNames: []string{"ws1", "ws2"},
		},
		{
			name:     "error: propagates db error",
			namesErr: errors.New("db error"),
			wantErr:  true,
		},
		{
			name:       "empty: returns empty slice not nil",
			names:      []string{},
			wantNames:  []string{},
			wantNonNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewMockGraphStore()
			mock.SetWorkspaceNamesResult(tc.names, tc.namesErr)
			svc := NewRAGService(nil, nil, mock, logger)

			got, err := svc.GetWorkspaceCollections(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNonNil && got == nil {
				t.Fatal("expected non-nil slice, got nil")
			}
			if len(got) != len(tc.wantNames) {
				t.Fatalf("len: want %d, got %d", len(tc.wantNames), len(got))
			}
			for i, name := range tc.wantNames {
				if got[i] != name {
					t.Errorf("index %d: want %q, got %q", i, name, got[i])
				}
			}
		})
	}
}
