package application

import (
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/stretchr/testify/require"
)

func TestCanonicalExtractedFactEntitiesAreOrderIndependent(t *testing.T) {
	req := &ExtractFactsRequest{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "ignored-for-user-scope",
		Scope: string(domain.ScopeUser), SourceMessageID: "message-1",
	}
	confidence := 0.8
	first := &port.ExtractedFact{
		Content: "User uses Go", Importance: 0.9, Confidence: &confidence,
		FactType: "skill", Entities: []string{" Go ", "PostgreSQL", "Go"},
	}
	second := &port.ExtractedFact{
		Content: "User uses Go", Importance: 0.9, Confidence: &confidence,
		FactType: "skill", Entities: []string{"PostgreSQL", "Go"},
	}

	a, err := canonicalizeExtractedFact(req, first, 3)
	require.NoError(t, err)
	b, err := canonicalizeExtractedFact(req, second, 3)
	require.NoError(t, err)
	require.Equal(t, []string{"Go", "PostgreSQL"}, a.Entities)
	require.Equal(t, a.Entities, b.Entities)
	require.Equal(t, a.PayloadHash, b.PayloadHash)
	require.True(t, strings.HasPrefix(a.PayloadHash, factPayloadHashPrefixV1))
	require.Equal(t, factPayloadHashPrefixV1+"f41ac27cb2f87c2862fa6dcfa6b992d9376b49f0c14ad4416343d416dfa0a29d", a.PayloadHash)
}

func TestCanonicalExtractedFactHashSeparatesIdentityAndPayload(t *testing.T) {
	base := &ExtractFactsRequest{TenantID: "tenant-1", UserID: "user-1", Scope: string(domain.ScopeUser), SourceMessageID: "message-1"}
	fact := &port.ExtractedFact{Content: "User uses Go", Importance: 0.9, FactType: "skill"}

	first, err := canonicalizeExtractedFact(base, fact, 0)
	require.NoError(t, err)
	changedPayload := *fact
	changedPayload.Content = "User uses Rust"
	second, err := canonicalizeExtractedFact(base, &changedPayload, 0)
	require.NoError(t, err)
	require.Equal(t, first.Identity, second.Identity)
	require.NotEqual(t, first.PayloadHash, second.PayloadHash)

	otherOrdinal, err := canonicalizeExtractedFact(base, fact, 1)
	require.NoError(t, err)
	require.NotEqual(t, first.Identity, otherOrdinal.Identity)
	require.NotEqual(t, first.PayloadHash, otherOrdinal.PayloadHash)
}

func TestCanonicalExtractedFactValidatesSourceOwnership(t *testing.T) {
	fact := &port.ExtractedFact{Content: "fact", Importance: 0.8, FactType: "other"}
	tests := []struct {
		name string
		req  ExtractFactsRequest
		ord  int
	}{
		{name: "missing message", req: ExtractFactsRequest{TenantID: "t", UserID: "u", Scope: "user"}, ord: 0},
		{name: "negative ordinal", req: ExtractFactsRequest{TenantID: "t", UserID: "u", Scope: "user", SourceMessageID: "m"}, ord: -1},
		{name: "agent owner required", req: ExtractFactsRequest{TenantID: "t", UserID: "u", Scope: "agent", SourceMessageID: "m"}, ord: 0},
		{name: "invalid scope", req: ExtractFactsRequest{TenantID: "t", UserID: "u", Scope: "shared", SourceMessageID: "m"}, ord: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := canonicalizeExtractedFact(&tc.req, fact, tc.ord)
			require.ErrorIs(t, err, domain.ErrInvalidFactSourceIdentity)
		})
	}
}

func TestCanonicalExtractedFactRejectsEmptyNormalizedEntity(t *testing.T) {
	req := &ExtractFactsRequest{TenantID: "t", UserID: "u", Scope: "user", SourceMessageID: "m"}
	_, err := canonicalizeExtractedFact(req, &port.ExtractedFact{Content: "fact", Importance: 0.8, Entities: []string{"  "}}, 0)
	require.True(t, errors.Is(err, domain.ErrInvalidFactSourceIdentity))
}

func TestOriginalOrdinalSurvivesQualityFilteringAndSorting(t *testing.T) {
	low, high := 0.1, 0.95
	facts := indexedExtractedFacts([]*port.ExtractedFact{
		{Content: "filtered", Importance: 0.9, Confidence: &low},
		{Content: "survives second", Importance: 0.7, Confidence: &high},
		{Content: "survives third", Importance: 0.8, Confidence: &high},
		{Content: "survives fourth", Importance: 0.6, Confidence: &high},
	})
	facts = qualityFilterAndSortExtractedFacts(facts, 2)

	require.Len(t, facts, 2)
	require.Equal(t, "survives third", facts[0].Fact.Content)
	require.Equal(t, 2, facts[0].OriginalOrdinal)
	require.Equal(t, "survives second", facts[1].Fact.Content)
	require.Equal(t, 1, facts[1].OriginalOrdinal)
}
