package textchunk

import (
	"context"
	"strings"
)

// defaultSeparators are tried in priority order until chunks fit within maxRunes.
var defaultSeparators = []string{"\n\n", "\n", "。", "！", "？", ". ", "! ", "? ", " ", ""}

// RecursiveStrategy splits text by trying separators from coarser to finer.
// Equivalent to LangChain's RecursiveCharacterTextSplitter.
type RecursiveStrategy struct {
	separators []string
}

func NewRecursiveStrategy() *RecursiveStrategy {
	return &RecursiveStrategy{separators: defaultSeparators}
}

func (s *RecursiveStrategy) Name() string { return "recursive" }

func (s *RecursiveStrategy) Chunk(_ context.Context, text string, maxRunes, overlapRunes int, _ Embedder) ChunkResult {
	leaves := s.split(text, s.separators, maxRunes, overlapRunes)
	for i := range leaves {
		leaves[i].Index = i
	}
	return ChunkResult{Leaves: leaves}
}

func (s *RecursiveStrategy) split(text string, seps []string, maxRunes, overlapRunes int) []TextChunk {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		if len(runes) == 0 {
			return nil
		}
		return []TextChunk{{Content: text}}
	}

	// Find the first separator that actually splits this text.
	sep := seps[len(seps)-1] // fallback: character-level
	var parts []string
	for _, candidate := range seps {
		if candidate == "" {
			break
		}
		if strings.Contains(text, candidate) {
			sep = candidate
			parts = strings.Split(text, sep)
			break
		}
	}
	if len(parts) == 0 {
		// Character-level split
		parts = charSplit(runes, maxRunes)
		sep = ""
	}

	// Merge small adjacent parts back up to maxRunes, with overlap.
	var chunks []TextChunk
	var current strings.Builder
	for _, part := range parts {
		partRunes := []rune(part)
		curRunes := []rune(current.String())

		if len(curRunes)+len(sep)+len(partRunes) > maxRunes && current.Len() > 0 {
			chunks = append(chunks, TextChunk{Content: strings.TrimSpace(current.String())})
			// Keep overlap from tail of current.
			if overlapRunes > 0 && len(curRunes) > overlapRunes {
				current.Reset()
				current.WriteString(string(curRunes[len(curRunes)-overlapRunes:]))
				current.WriteString(sep)
			} else {
				current.Reset()
			}
		}
		if current.Len() > 0 {
			current.WriteString(sep)
		}
		current.WriteString(part)
	}
	if current.Len() > 0 {
		tail := strings.TrimSpace(current.String())
		if len([]rune(tail)) > 0 {
			chunks = append(chunks, TextChunk{Content: tail})
		}
	}

	// Recursively split any chunk still over limit using finer separators.
	if len(seps) <= 1 {
		return chunks
	}
	var result []TextChunk
	for _, c := range chunks {
		if len([]rune(c.Content)) > maxRunes {
			result = append(result, s.split(c.Content, seps[1:], maxRunes, overlapRunes)...)
		} else {
			result = append(result, c)
		}
	}
	return result
}

// charSplit slices runes into pieces of at most maxRunes each.
func charSplit(runes []rune, maxRunes int) []string {
	var out []string
	for i := 0; i < len(runes); i += maxRunes {
		end := min(i+maxRunes, len(runes))
		out = append(out, string(runes[i:end]))
	}
	return out
}
