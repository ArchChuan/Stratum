package textchunk

import (
	"context"
	"strings"
	"testing"
)

type stubEmbedder struct {
	vecs [][]float32
	err  error
}

func (e *stubEmbedder) EmbedVector(_ context.Context, _ string) ([]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	if len(e.vecs) == 0 {
		return nil, nil
	}
	vec := e.vecs[0]
	if len(e.vecs) > 1 {
		e.vecs = e.vecs[1:]
	}
	return vec, nil
}

func TestSemanticStrategyName(t *testing.T) {
	if got := NewSemanticStrategy().Name(); got != "semantic" {
		t.Errorf("Name() = %q, want semantic", got)
	}
}

func TestSemanticNilEmbedderFallsBack(t *testing.T) {
	// 极端情况：embedder 为 nil 必须退化为 recursive。
	res := NewSemanticStrategy().Chunk(context.Background(), "hello world", 100, 10, nil)
	if len(res.Leaves) != 1 {
		t.Errorf("expected fallback single leaf, got %d", len(res.Leaves))
	}
}

func TestSemanticTooFewSentencesFallsBack(t *testing.T) {
	// 极端情况：句子数不足时直接 recursive。
	res := NewSemanticStrategy().Chunk(context.Background(), "只有一个句子。", 100, 10, &stubEmbedder{vecs: [][]float32{{1, 0}}})
	if len(res.Leaves) != 1 {
		t.Errorf("expected fallback leaf, got %d", len(res.Leaves))
	}
}

func TestSemanticEmbedErrorFallsBack(t *testing.T) {
	// 极端情况：embedding 失败必须 fallback 而非返回部分结果。
	res := NewSemanticStrategy().Chunk(context.Background(), "句子一。句子二。句子三。", 100, 10, &stubEmbedder{err: context.DeadlineExceeded})
	if len(res.Leaves) == 0 {
		t.Error("expected fallback to produce leaves")
	}
}

func TestSemanticSplitsOnSimilarityDrop(t *testing.T) {
	// 相邻向量相似度高不分裂、低则分裂。
	embedder := &stubEmbedder{vecs: [][]float32{
		{1, 0, 0}, // s1
		{1, 0, 0}, // s2 相似 → 不分裂
		{0, 1, 0}, // s3 不相似 → 分裂
		{0, 1, 0}, // s4 相似 → 不分裂
	}}
	text := "alpha beta. gamma delta. epsilon zeta. eta theta."
	res := NewSemanticStrategy().Chunk(context.Background(), text, 1000, 10, embedder)
	// 4 个句子，1 个分裂点 → 2 个 chunk。
	if len(res.Leaves) != 2 {
		t.Errorf("expected 2 leaves, got %d", len(res.Leaves))
	}
	if !strings.Contains(res.Leaves[0].Content, "alpha") || !strings.Contains(res.Leaves[1].Content, "epsilon") {
		t.Errorf("unexpected chunk contents: %q / %q", res.Leaves[0].Content, res.Leaves[1].Content)
	}
}

func TestSemanticChunkOverMaxRunesRecursivelySplit(t *testing.T) {
	// 合并后 chunk 超长 → recursive 再拆分。
	embedder := &stubEmbedder{vecs: [][]float32{{1, 0}, {1, 0}}}
	text := strings.Repeat("word", 60) + ". " + strings.Repeat("more", 60) + "."
	res := NewSemanticStrategy().Chunk(context.Background(), text, 100, 10, embedder)
	if len(res.Leaves) < 2 {
		t.Errorf("expected recursive sub-split, got %d leaves", len(res.Leaves))
	}
	for _, c := range res.Leaves {
		if len([]rune(c.Content)) > 100 {
			t.Errorf("leaf exceeds maxRunes: %d", len([]rune(c.Content)))
		}
	}
}

func TestSplitIntoSentences(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"chinese terminals", "你好。世界！好吗？", 3},
		{"latin terminals", "Hi. There! Yes?", 3},
		{"trailing no punctuation", "Hello world", 1},
		{"blank segments dropped", "a.  b. ", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitIntoSentences(tc.text); len(got) != tc.want {
				t.Errorf("splitIntoSentences(%q) = %d, want %d (%v)", tc.text, len(got), tc.want, got)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0}, []float32{1, 0}, 1.0},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0.0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"mixed", []float32{1, 2}, []float32{2, 1}, 0.8},
		{"empty a", nil, []float32{1, 0}, 0.0},
		{"empty b", []float32{1, 0}, nil, 0.0},
		{"length mismatch", []float32{1}, []float32{1, 0}, 0.0},
		{"zero vector", []float32{0, 0}, []float32{1, 0}, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cosineSimilarity(tc.a, tc.b)
			if got-tc.want > 1e-9 || tc.want-got > 1e-9 {
				t.Errorf("cosineSimilarity = %v, want %v", got, tc.want)
			}
		})
	}
}
