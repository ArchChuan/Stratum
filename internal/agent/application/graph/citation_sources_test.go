package graph

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func src(chunkID string) port.RAGSearchSource {
	return port.RAGSearchSource{ChunkID: chunkID, DocumentID: "doc-" + chunkID}
}

func TestAppendCitationSourcesDeduplicatesByChunkID(t *testing.T) {
	s := &ReActState{}
	s.appendCitationSources(port.RAGSearchEvidence{Sources: []port.RAGSearchSource{src("c1"), src("c2")}})
	// Same chunk in a later search: newest search wins, no duplicate.
	s.appendCitationSources(port.RAGSearchEvidence{Sources: []port.RAGSearchSource{src("c1"), src("c3")}})

	if len(s.CitationSources) != 3 {
		t.Fatalf("CitationSources = %d, want 3 (deduplicated)", len(s.CitationSources))
	}
	if s.CitationSources[0].ChunkID != "c1" || s.CitationSources[2].ChunkID != "c3" {
		t.Fatalf("order wrong: %+v", s.CitationSources)
	}
}

func TestAppendCitationSourcesCapsAtMaxAndKeepsNewest(t *testing.T) {
	s := &ReActState{}
	for i := 0; i < constants.MaxAgentResultSources+5; i++ {
		s.appendCitationSources(port.RAGSearchEvidence{Sources: []port.RAGSearchSource{src(string(rune('a'+i%26)) + string(rune('0'+i)))}})
	}
	if len(s.CitationSources) != constants.MaxAgentResultSources {
		t.Fatalf("CitationSources = %d, want cap %d", len(s.CitationSources), constants.MaxAgentResultSources)
	}
	// Cap keeps the newest search's contribution.
	if s.CitationSources[len(s.CitationSources)-1].ChunkID == "" {
		t.Fatalf("cap dropped newest source: %+v", s.CitationSources)
	}
}

func TestAppendCitationSourcesSkipsEmpty(t *testing.T) {
	s := &ReActState{}
	s.appendCitationSources(port.RAGSearchEvidence{})
	if len(s.CitationSources) != 0 {
		t.Fatalf("empty evidence must not grow citations: %+v", s.CitationSources)
	}
	// Empty chunk IDs are skipped (they cannot be deduplicated or cited).
	s.appendCitationSources(port.RAGSearchEvidence{Sources: []port.RAGSearchSource{{ChunkID: ""}}})
	if len(s.CitationSources) != 0 {
		t.Fatalf("empty chunk ID must be skipped: %+v", s.CitationSources)
	}
}
