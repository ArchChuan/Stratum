package textchunk

import (
	"context"
	"strings"
	"testing"
)

func TestStructureRecursiveStrategyName(t *testing.T) {
	if got := NewStructureRecursiveStrategy().Name(); got != "structure_recursive" {
		t.Errorf("Name() = %q, want structure_recursive", got)
	}
}

func TestStructureNoHeadersFallsBack(t *testing.T) {
	// 极端情况：无 markdown header 时退化为纯 recursive。
	res := NewStructureRecursiveStrategy().Chunk(context.Background(), strings.Repeat("a", 250), 100, 0, nil)
	if len(res.Leaves) != 3 {
		t.Errorf("expected 3 recursive leaves, got %d", len(res.Leaves))
	}
	if len(res.Parents) != 0 {
		t.Errorf("expected no parents, got %d", len(res.Parents))
	}
}

func TestStructureSectionsBecomeParents(t *testing.T) {
	text := "# Title\n" + strings.Repeat("a", 50) + "\n## Sub\n" + strings.Repeat("b", 50)
	res := NewStructureRecursiveStrategy().Chunk(context.Background(), text, 100, 10, nil)
	if len(res.Parents) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(res.Parents))
	}
	// 注意：当前实现 parent 只含 section 正文，header 不进 parent（已知缺陷，见报告）。
	if !strings.Contains(res.Parents[0].Content, strings.Repeat("a", 50)) {
		t.Errorf("parent 0 missing section body: %q", res.Parents[0].Content)
	}
	// 子块必须引用 parent。
	if len(res.Leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(res.Leaves))
	}
	for i, leaf := range res.Leaves {
		if leaf.ParentID == "" {
			t.Errorf("leaf %d missing ParentID", i)
		}
		if leaf.Index != i {
			t.Errorf("leaf %d Index = %d", i, leaf.Index)
		}
	}
}

func TestStructureHugeSectionCapped(t *testing.T) {
	// 极端情况：section 超过 maxRunes*2 时 parent 截断并加省略号。
	// 注意：单个 header 会被 len(sections)<=1 错误 fallback（已知缺陷），需第二个 header 保持 parent 路径。
	text := "# Big\n" + strings.Repeat("x", 500) + "\n# Tail\nsmall"
	res := NewStructureRecursiveStrategy().Chunk(context.Background(), text, 100, 10, nil)
	if len(res.Parents) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(res.Parents))
	}
	if !strings.HasSuffix(res.Parents[0].Content, "...") {
		t.Errorf("expected truncated parent with ..., got %q", res.Parents[0].Content)
	}
	if len([]rune(res.Parents[0].Content)) > 2*100+3 {
		t.Errorf("parent too long: %d runes", len([]rune(res.Parents[0].Content)))
	}
}

func TestStructureEmptySectionSkipped(t *testing.T) {
	// 极端情况：header 后紧跟另一 header 且无内容时，空 section 被 continue 跳过。
	// "# A\n## B"（无尾换行）使 section 2 的 content 为空 → 只产生 1 个 parent。
	res := NewStructureRecursiveStrategy().Chunk(context.Background(), "# A\n## B", 100, 10, nil)
	if len(res.Parents) != 1 {
		t.Errorf("expected 1 parent, got %d", len(res.Parents))
	}
	// 空 section 被跳过，但 content="\n" 的 section 仍产生 1 个空白 leaf（既有行为）。
	if len(res.Leaves) != 1 {
		t.Errorf("expected 1 leaf, got %d", len(res.Leaves))
	}
}

func TestMakeID(t *testing.T) {
	if got := makeID("parent", 0); got != "parent_0" {
		t.Errorf("makeID = %q, want parent_0", got)
	}
	if got := makeID("parent", 42); got != "parent_42" {
		t.Errorf("makeID = %q, want parent_42", got)
	}
}

func TestSplitBySectionsPreambleAndTrailing(t *testing.T) {
	s := NewStructureRecursiveStrategy()
	// preamble 文本 + header + trailing 文本。
	text := "preamble text\n# H\nbody\n"
	sections := s.splitBySections(text)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if !strings.Contains(sections[0].content, "preamble") {
		t.Errorf("preamble missing: %+v", sections[0])
	}
	if !strings.Contains(sections[1].content, "body") {
		t.Errorf("trailing body missing: %+v", sections[1])
	}
}
