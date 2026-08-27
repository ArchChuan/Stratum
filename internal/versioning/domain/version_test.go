package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionComputeContentHashIsCanonicalAndStable(t *testing.T) {
	a := Version{Payload: map[string]any{"name": "assistant", "temperature": 0.7, "nested": map[string]any{"b": 1, "a": 2}}}
	b := Version{Payload: map[string]any{"nested": map[string]any{"a": 2, "b": 1}, "temperature": 0.7, "name": "assistant"}}

	ha, err := a.ComputeContentHash()
	require.NoError(t, err)
	hb, err := b.ComputeContentHash()
	require.NoError(t, err)
	// Go 的 encoding/json 对 map 键排序:同一 payload 无论插入顺序 hash 一致,
	// 作为内容指纹与去重基线。
	require.Equal(t, ha, hb)
	require.NotEmpty(t, ha)
}

func TestVersionComputeContentHashEmptyPayload(t *testing.T) {
	hash, err := (Version{}).ComputeContentHash()
	require.NoError(t, err)
	// 空 payload canonical JSON 为 {}。
	require.NotEmpty(t, hash)
}

func TestVersionComputeContentHashUnmarshalablePayloadFails(t *testing.T) {
	v := Version{Payload: map[string]any{"bad": func() {}}}
	_, err := v.ComputeContentHash()
	require.Error(t, err)
}
