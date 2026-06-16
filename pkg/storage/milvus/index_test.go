package milvus_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

func TestIndexImplementsVectorIndex(t *testing.T) {
	var _ milvus.VectorIndex = (*milvus.Index)(nil)
	t.Log("Index satisfies VectorIndex")
}

func TestNewIndex_Nil(t *testing.T) {
	idx := milvus.NewIndex(nil)
	if idx == nil {
		t.Fatal("NewIndex returned nil")
	}
}
