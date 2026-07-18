package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const factPayloadHashPrefixV1 = "memory-fact-payload:v1:sha256:"

type indexedExtractedFact struct {
	Fact            *port.ExtractedFact
	OriginalOrdinal int
}

type canonicalExtractedFact struct {
	Identity    domain.FactSourceIdentity
	Entities    []string
	PayloadHash string
}

func indexedExtractedFacts(facts []*port.ExtractedFact) []indexedExtractedFact {
	result := make([]indexedExtractedFact, len(facts))
	for i, fact := range facts {
		result[i] = indexedExtractedFact{Fact: fact, OriginalOrdinal: i}
	}
	return result
}

func qualityFilterAndSortExtractedFacts(facts []indexedExtractedFact, limit int) []indexedExtractedFact {
	filtered := facts[:0]
	for _, item := range facts {
		if item.Fact != nil && effectiveConfidence(item.Fact) >= constants.FactConfidenceMin {
			filtered = append(filtered, item)
		}
	}
	if limit > 0 && len(filtered) > limit {
		sort.SliceStable(filtered, func(i, j int) bool {
			ci, cj := effectiveConfidence(filtered[i].Fact), effectiveConfidence(filtered[j].Fact)
			if ci != cj {
				return ci > cj
			}
			return filtered[i].Fact.Importance > filtered[j].Fact.Importance
		})
		filtered = filtered[:limit]
	}
	return filtered
}

func canonicalizeExtractedFact(req *ExtractFactsRequest, fact *port.ExtractedFact, ordinal int) (*canonicalExtractedFact, error) {
	if req == nil || fact == nil || req.TenantID == "" || req.UserID == "" || req.SourceMessageID == "" || ordinal < 0 {
		return nil, domain.ErrInvalidFactSourceIdentity
	}
	if req.Scope != string(domain.ScopeUser) && req.Scope != string(domain.ScopeAgent) {
		return nil, domain.ErrInvalidFactSourceIdentity
	}
	ownerAgentID := ""
	if req.Scope == string(domain.ScopeAgent) {
		if req.AgentID == "" {
			return nil, domain.ErrInvalidFactSourceIdentity
		}
		ownerAgentID = req.AgentID
	}
	entities, err := canonicalEntityNames(fact.Entities)
	if err != nil {
		return nil, err
	}
	identity := domain.FactSourceIdentity{MessageID: req.SourceMessageID, TaskID: req.SourceTaskID, Ordinal: ordinal}
	payload := struct {
		Version, TenantID, UserID, Scope, AgentID, MessageID string
		Ordinal                                              int
		Content                                              string
		Importance, Confidence                               float64
		Category, Source                                     string
		Entities                                             []string
	}{
		Version: "v1", TenantID: req.TenantID, UserID: req.UserID, Scope: req.Scope,
		AgentID: ownerAgentID, MessageID: req.SourceMessageID, Ordinal: ordinal,
		Content: fact.Content, Importance: fact.Importance, Confidence: effectiveConfidence(fact),
		Category: domain.FactTypeToCategory(fact.FactType), Source: domain.FactSourceLLMExtraction,
		Entities: entities,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	hash := factPayloadHashPrefixV1 + hex.EncodeToString(sum[:])
	return &canonicalExtractedFact{Identity: identity, Entities: entities, PayloadHash: hash}, nil
}

func canonicalEntityNames(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, domain.ErrInvalidFactSourceIdentity
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}
