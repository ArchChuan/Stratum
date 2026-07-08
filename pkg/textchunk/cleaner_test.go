package textchunk

import (
	"strings"
	"testing"
)

func TestTextCleaner_Clean(t *testing.T) {
	tc := NewTextCleaner()

	tests := []struct {
		name  string
		input string
		check func(t *testing.T, got string)
	}{
		{
			name:  "strips control characters",
			input: "hello\x00world\x07end\x1b",
			check: func(t *testing.T, got string) {
				if strings.ContainsAny(got, "\x00\x07\x1b") {
					t.Errorf("control chars not stripped: %q", got)
				}
				if !strings.Contains(got, "helloworldend") {
					t.Errorf("content missing: %q", got)
				}
			},
		},
		{
			name:  "collapses 3+ newlines to 2",
			input: "para1\n\n\n\n\npara2",
			check: func(t *testing.T, got string) {
				if strings.Contains(got, "\n\n\n") {
					t.Errorf("excessive newlines not collapsed: %q", got)
				}
				if !strings.Contains(got, "para1\n\npara2") {
					t.Errorf("content missing: %q", got)
				}
			},
		},
		{
			name:  "trims leading and trailing whitespace",
			input: "  \n\nhello world\n\n  ",
			check: func(t *testing.T, got string) {
				if got != strings.TrimSpace(got) {
					t.Errorf("not trimmed: %q", got)
				}
			},
		},
		{
			name:  "removes Chinese page footer",
			input: "正文内容\n第3页 共10页\n更多内容",
			check: func(t *testing.T, got string) {
				if strings.Contains(got, "第3页") {
					t.Errorf("header/footer not removed: %q", got)
				}
				if !strings.Contains(got, "正文内容") || !strings.Contains(got, "更多内容") {
					t.Errorf("content missing after footer removal: %q", got)
				}
			},
		},
		{
			name:  "removes English page footer",
			input: "content\nPage 3 of 10\nmore content",
			check: func(t *testing.T, got string) {
				if strings.Contains(got, "Page 3 of 10") {
					t.Errorf("English page footer not removed: %q", got)
				}
			},
		},
		{
			name:  "preserves tabs and carriage returns",
			input: "col1\tcol2\r\ncol3\tcol4",
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "\t") {
					t.Errorf("tab stripped unexpectedly: %q", got)
				}
			},
		},
		{
			name:  "empty input returns empty",
			input: "",
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tc.Clean(tt.input)
			tt.check(t, got)
		})
	}
}

func TestTextCleaner_FilterChunks_PureNonContent(t *testing.T) {
	tc := NewTextCleaner()
	chunks := []TextChunk{
		{Content: "这是有效内容，包含足够的文字", Index: 0},
		{Content: "---===---", Index: 1}, // pure punctuation
		{Content: "   \n\t  ", Index: 2}, // pure whitespace
		{Content: "!!!???...", Index: 3}, // pure punctuation
		{Content: "另一段有效内容需要足够长度", Index: 4},
	}

	got := tc.FilterChunks(chunks)

	if len(got) != 2 {
		t.Errorf("expected 2 chunks, got %d: %v", len(got), got)
	}
}

func TestTextCleaner_FilterChunks_MinContentRunes(t *testing.T) {
	tc := NewTextCleaner()
	chunks := []TextChunk{
		{Content: "ok", Index: 0},            // too short (2 letters)
		{Content: "这段文字足够长可以通过检测", Index: 1}, // sufficient
		{Content: "ab", Index: 2},            // too short
	}

	got := tc.FilterChunks(chunks)

	if len(got) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(got))
	}
}

func TestTextCleaner_FilterChunks_NearDuplicate(t *testing.T) {
	tc := NewTextCleaner()

	// Two chunks that are identical — exact duplicate must be filtered.
	text := "人工智能是计算机科学的一个分支，致力于创建能够执行通常需要人类智慧的任务的智能系统和程序。"
	distinct := "量子计算利用量子力学现象如叠加和纠缠来执行数据处理，与传统计算机架构有根本不同之处。"

	chunks := []TextChunk{
		{Content: text, Index: 0},
		{Content: text, Index: 1}, // exact duplicate
		{Content: distinct, Index: 2},
	}

	got := tc.FilterChunks(chunks)

	if len(got) != 2 {
		t.Errorf("expected 2 chunks (exact dup removed), got %d", len(got))
	}
}

func TestTextCleaner_FilterChunks_ExactDuplicate(t *testing.T) {
	tc := NewTextCleaner()
	text := "完全相同的内容文字重复两次应该被去除掉只保留一个"
	chunks := []TextChunk{
		{Content: text, Index: 0},
		{Content: text, Index: 1},
	}

	got := tc.FilterChunks(chunks)

	if len(got) != 1 {
		t.Errorf("expected 1 chunk after dedup, got %d", len(got))
	}
}

func TestHammingDistance64(t *testing.T) {
	tests := []struct {
		a, b uint64
		want int
	}{
		{0, 0, 0},
		{0xFFFFFFFFFFFFFFFF, 0, 64},
		{0b1010, 0b0101, 4},
		{1, 3, 1}, // differ in bit 1 only
	}
	for _, tt := range tests {
		got := hammingDistance64(tt.a ^ tt.b)
		if got != tt.want {
			t.Errorf("hammingDistance64(%b ^ %b) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
