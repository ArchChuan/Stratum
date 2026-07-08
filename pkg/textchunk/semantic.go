package textchunk

import (
	"context"
	"math"
	"strings"
)

const (
	semanticSplitThreshold = 0.7 // cosine similarity drop below this triggers a split
	semanticMinSentences   = 2   // minimum sentences per chunk
)

// SemanticStrategy groups sentences into chunks by detecting where embedding
// similarity drops sharply — each drop is a semantic boundary.
// Falls back to RecursiveStrategy when embedder is nil.
type SemanticStrategy struct {
	fallback *RecursiveStrategy
}

func NewSemanticStrategy() *SemanticStrategy {
	return &SemanticStrategy{fallback: NewRecursiveStrategy()}
}

func (s *SemanticStrategy) Name() string { return "semantic" }

func (s *SemanticStrategy) Chunk(ctx context.Context, text string, maxRunes, overlapRunes int, embedder Embedder) ChunkResult {
	if embedder == nil {
		return s.fallback.Chunk(ctx, text, maxRunes, overlapRunes, nil)
	}

	sentences := splitIntoSentences(text)
	if len(sentences) < semanticMinSentences*2 {
		// Too few sentences to split semantically; use recursive.
		return s.fallback.Chunk(ctx, text, maxRunes, overlapRunes, nil)
	}

	// Embed each sentence (best-effort: skip errors, keep sentence).
	vecs := make([][]float32, len(sentences))
	for i, sent := range sentences {
		vec, err := embedder.EmbedVector(ctx, sent)
		if err != nil {
			// Embedding failed — fallback to recursive for this document.
			return s.fallback.Chunk(ctx, text, maxRunes, overlapRunes, nil)
		}
		vecs[i] = vec
	}

	// Compute consecutive cosine similarities and find split points.
	var splitAt []int
	for i := 1; i < len(sentences); i++ {
		sim := cosineSimilarity(vecs[i-1], vecs[i])
		if sim < semanticSplitThreshold {
			splitAt = append(splitAt, i)
		}
	}

	// Build chunks from split points, respecting maxRunes.
	var leaves []TextChunk
	start := 0
	for _, sp := range append(splitAt, len(sentences)) {
		chunk := strings.Join(sentences[start:sp], " ")
		// If the merged chunk is still too long, recursively split it.
		if len([]rune(chunk)) > maxRunes {
			sub := s.fallback.split(chunk, defaultSeparators, maxRunes, overlapRunes)
			for _, sc := range sub {
				sc.Index = len(leaves)
				leaves = append(leaves, sc)
			}
		} else {
			leaves = append(leaves, TextChunk{Content: strings.TrimSpace(chunk), Index: len(leaves)})
		}
		start = sp
	}

	return ChunkResult{Leaves: leaves}
}

// splitIntoSentences splits text into sentences using common terminal punctuation.
func splitIntoSentences(text string) []string {
	var sents []string
	var cur strings.Builder
	for _, r := range text {
		cur.WriteRune(r)
		switch r {
		case '。', '！', '？', '.', '!', '?':
			s := strings.TrimSpace(cur.String())
			if s != "" {
				sents = append(sents, s)
			}
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		if s := strings.TrimSpace(cur.String()); s != "" {
			sents = append(sents, s)
		}
	}
	return sents
}

// cosineSimilarity returns the cosine similarity between two vectors.
// Returns 0 for zero-length vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
