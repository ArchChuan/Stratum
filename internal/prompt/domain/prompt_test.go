package domain

import (
	"strings"
	"testing"
)

func TestComputeHashDeterministic(t *testing.T) {
	// 同一内容两次计算必须一致。
	content := "You are a helpful assistant."
	if first := ComputeHash(content); first != ComputeHash(content) {
		t.Error("expected hash deterministic")
	}
}

func TestComputeHashKnownVector(t *testing.T) {
	// 已知向量：SHA-256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	if got := ComputeHash("hello"); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("expected known SHA-256 of hello, got %q", got)
	}
}

func TestComputeHashEmptyString(t *testing.T) {
	// 极端情况：空内容仍是合法 hash（SHA-256 空串已知向量）。
	if got := ComputeHash(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("expected known SHA-256 of empty string, got %q", got)
	}
}

func TestComputeHashDiffersByContent(t *testing.T) {
	// 极端情况：仅空白/大小写差异也必须产生不同 hash。
	a, b, c := ComputeHash("prompt"), ComputeHash("prompt "), ComputeHash("PROMPT")
	if a == b || a == c || b == c {
		t.Error("expected distinct hashes for distinct contents")
	}
}

func TestComputeHashFormat(t *testing.T) {
	// hash 必须是 64 位小写十六进制。
	hash := ComputeHash("anything")
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}
	for _, r := range hash {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Errorf("non-hex char %q in hash", r)
		}
	}
}
