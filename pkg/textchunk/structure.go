package textchunk

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Markdown header patterns: #, ##, ###, etc.
var reMarkdownHeader = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

// StructureRecursiveStrategy detects Markdown headers to split into sections,
// then applies recursive splitting within each section. Each section is a
// potential parent; recursive chunks within it are children.
type StructureRecursiveStrategy struct {
	inner *RecursiveStrategy
}

func NewStructureRecursiveStrategy() *StructureRecursiveStrategy {
	return &StructureRecursiveStrategy{inner: NewRecursiveStrategy()}
}

func (s *StructureRecursiveStrategy) Name() string { return "structure_recursive" }

func (s *StructureRecursiveStrategy) Chunk(ctx context.Context, text string, maxRunes, overlapRunes int, embedder Embedder) ChunkResult {
	sections := s.splitBySections(text)
	if len(sections) <= 1 {
		// No headers found; fallback to pure recursive.
		return s.inner.Chunk(ctx, text, maxRunes, overlapRunes, embedder)
	}

	// Each section becomes a parent; chunks within it are children.
	var leaves, parents []TextChunk
	parentIdx := 0
	for _, sec := range sections {
		secRunes := []rune(sec.content)
		if len(secRunes) == 0 {
			continue
		}

		// Parent = full section (or capped at 2x maxRunes if huge).
		parentID := makeID("parent", parentIdx)
		parentContent := sec.content
		if len(secRunes) > maxRunes*2 {
			parentContent = string(secRunes[:maxRunes*2]) + "..."
		}
		parents = append(parents, TextChunk{
			Content: strings.TrimSpace(parentContent),
			Index:   parentIdx,
		})

		// Children = recursive split of this section.
		childChunks := s.inner.split(sec.content, s.inner.separators, maxRunes, overlapRunes)
		for i, child := range childChunks {
			child.Index = len(leaves) + i
			child.ParentID = parentID
			leaves = append(leaves, child)
		}
		parentIdx++
	}

	return ChunkResult{Leaves: leaves, Parents: parents}
}

type section struct {
	header  string
	content string
}

func (s *StructureRecursiveStrategy) splitBySections(text string) []section {
	matches := reMarkdownHeader.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []section{{content: text}}
	}

	var sections []section
	prevEnd := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		// Content before this header (belongs to previous section or preamble).
		if start > prevEnd {
			if len(sections) > 0 {
				sections[len(sections)-1].content += text[prevEnd:start]
			} else {
				sections = append(sections, section{content: text[prevEnd:start]})
			}
		}
		// New section starting with this header.
		sections = append(sections, section{
			header:  text[start:end],
			content: "",
		})
		prevEnd = end
	}
	// Trailing content after last header.
	if prevEnd < len(text) && len(sections) > 0 {
		sections[len(sections)-1].content += text[prevEnd:]
	}

	return sections
}

func makeID(prefix string, idx int) string {
	return prefix + "_" + strconv.Itoa(idx)
}
