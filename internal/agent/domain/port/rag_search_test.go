package port_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/assert"
)

type mockRAGSearchProvider struct {
	out string
	err error
}

func (m *mockRAGSearchProvider) SearchKnowledge(_ context.Context, _ string, _ []string, _ string, _ int) (string, error) {
	return m.out, m.err
}

func TestRAGSearchProvider_InterfaceContract(t *testing.T) {
	var _ port.RAGSearchProvider = (*mockRAGSearchProvider)(nil)

	t.Run("returns context block on success", func(t *testing.T) {
		m := &mockRAGSearchProvider{out: "chunk-1\nchunk-2"}
		out, err := m.SearchKnowledge(context.Background(), "tenant-1", []string{"ws-a"}, "what is x", 5)
		assert.NoError(t, err)
		assert.Equal(t, "chunk-1\nchunk-2", out)
	})

	t.Run("propagates error", func(t *testing.T) {
		want := errors.New("vector backend down")
		m := &mockRAGSearchProvider{err: want}
		out, err := m.SearchKnowledge(context.Background(), "tenant-1", nil, "q", 0)
		assert.Equal(t, "", out)
		assert.ErrorIs(t, err, want)
	})
}
