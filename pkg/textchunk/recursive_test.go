package textchunk

import (
	"context"
	"strings"
	"testing"
)

func TestRecursiveStrategyName(t *testing.T) {
	if got := NewRecursiveStrategy().Name(); got != "recursive" {
		t.Errorf("Name() = %q, want recursive", got)
	}
}

func TestRecursiveChunkShortText(t *testing.T) {
	// 极端情况：文本不超过 maxRunes 时原样返回单块。
	res := NewRecursiveStrategy().Chunk(context.Background(), "hello world", 100, 10, nil)
	if len(res.Leaves) != 1 || res.Leaves[0].Content != "hello world" {
		t.Errorf("unexpected leaves: %+v", res.Leaves)
	}
	if res.Leaves[0].Index != 0 {
		t.Errorf("Index = %d, want 0", res.Leaves[0].Index)
	}
}

func TestRecursiveChunkEmptyText(t *testing.T) {
	// 极端情况：空文本返回 nil，不能产生空 chunk。
	res := NewRecursiveStrategy().Chunk(context.Background(), "", 100, 10, nil)
	if res.Leaves != nil {
		t.Errorf("expected nil leaves, got %+v", res.Leaves)
	}
}

func TestRecursiveSplitAtNewline(t *testing.T) {
	// 按段落分隔符拆分。
	text := strings.Repeat("a", 60) + "\n" + strings.Repeat("b", 60)
	res := NewRecursiveStrategy().Chunk(context.Background(), text, 100, 10, nil)
	if len(res.Leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(res.Leaves))
	}
	if res.Leaves[0].Content != strings.Repeat("a", 60) {
		t.Errorf("leaf 0 = %q", res.Leaves[0].Content)
	}
}

func TestRecursiveSplitCharLevelNoSeparator(t *testing.T) {
	// 极端情况：无任何分隔符命中 → 字符级拆分。
	strategy := NewRecursiveStrategy()
	long := strings.Repeat("x", 250)
	res := strategy.Chunk(context.Background(), long, 100, 0, nil)
	// 250 字符无分隔符：先字符级拆成 100/100/50，递归无更细分隔符可命中。
	if len(res.Leaves) != 3 {
		t.Fatalf("expected 3 leaves, got %d", len(res.Leaves))
	}
	for _, c := range res.Leaves {
		if len([]rune(c.Content)) > 100 {
			t.Errorf("leaf too long: %d runes", len([]rune(c.Content)))
		}
	}
}

func TestRecursiveSplitWithOverlap(t *testing.T) {
	// 极端情况：overlap > 0 时相邻 chunk 尾部重叠。
	strategy := NewRecursiveStrategy()
	text := strings.Repeat("a", 80) + "\n" + strings.Repeat("b", 80) + "\n" + strings.Repeat("c", 80)
	res := strategy.Chunk(context.Background(), text, 100, 20, nil)
	if len(res.Leaves) < 2 {
		t.Fatalf("expected multiple leaves, got %d", len(res.Leaves))
	}
	first, second := res.Leaves[0].Content, res.Leaves[1].Content
	if !strings.HasSuffix(first, strings.Repeat("a", 20)) {
		t.Errorf("expected overlap tail in first chunk: %q", first)
	}
	if !strings.HasPrefix(second, strings.Repeat("a", 20)) {
		t.Errorf("expected overlap head in second chunk: %q", second)
	}
}

func TestRecursiveSplitRecursesOnFinerSeparators(t *testing.T) {
	// 长段落含句号：段落分隔符不命中时递归到句子分隔符。
	strategy := NewRecursiveStrategy()
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString(strings.Repeat("a", 40) + "。")
	}
	text := sb.String() // 无换行，只有中文句号
	res := strategy.Chunk(context.Background(), text, 100, 0, nil)
	if len(res.Leaves) < 2 {
		t.Fatalf("expected multiple leaves, got %d", len(res.Leaves))
	}
	for _, c := range res.Leaves {
		if runes := len([]rune(c.Content)); runes > 100 {
			t.Errorf("leaf %q exceeds maxRunes: %d", c.Content, runes)
		}
	}
}

func TestRecursiveChunkIndexSequential(t *testing.T) {
	strategy := NewRecursiveStrategy()
	res := strategy.Chunk(context.Background(), strings.Repeat("x", 250), 100, 0, nil)
	for i, c := range res.Leaves {
		if c.Index != i {
			t.Errorf("leaf %d has Index %d", i, c.Index)
		}
	}
}
