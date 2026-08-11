package milvus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
)

// fakeSDKClient satisfies the Milvus SDK client.Client interface by embedding
// it and overriding only ListCollections. The embedded interface promotes the
// remaining methods (nil-receiver panics if ever called), which is acceptable
// for wrapper unit tests that only exercise ListCollections.
type fakeSDKClient struct {
	client.Client
	collections []*entity.Collection
	listErr     error
}

func (f *fakeSDKClient) ListCollections(ctx context.Context, opts ...client.ListCollectionOption) ([]*entity.Collection, error) {
	return f.collections, f.listErr
}

func newListCollectionsVectorStore(f *fakeSDKClient) *VectorStore {
	return &VectorStore{client: f, logger: zap.NewNop()}
}

func TestVectorStore_ListCollectionsFiltersByPrefixAndSorts(t *testing.T) {
	vs := newListCollectionsVectorStore(&fakeSDKClient{collections: []*entity.Collection{
		{Name: "memory_t1_text_embedding_v2"},
		{Name: "kb_ws1_embedding_3"},
		{Name: "memory_t1_b"},
		{Name: "memory_t10_x"}, // 尾下划线隔离：t1 前缀不得匹配 t10
		{Name: "memory_t1_a"},
		{Name: "memory_t1"}, // 无尾下划线形式是 legacy 名，不属于模型后缀前缀
		nil,                 // 异常防御：nil 项跳过
	}})

	got, err := vs.ListCollections(context.Background(), "memory_t1_")
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	want := []string{"memory_t1_a", "memory_t1_b", "memory_t1_text_embedding_v2"}
	if len(got) != len(want) {
		t.Fatalf("collections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collections = %v, want %v", got, want)
		}
	}

	// 空前缀 → 全部（排序后）。
	vs2 := newListCollectionsVectorStore(&fakeSDKClient{collections: []*entity.Collection{
		{Name: "b"}, {Name: "a"},
	}})
	got, err = vs2.ListCollections(context.Background(), "")
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("empty prefix = %v, %v", got, err)
	}
}

func TestVectorStore_ListCollectionsPropagatesError(t *testing.T) {
	vs := newListCollectionsVectorStore(&fakeSDKClient{listErr: errors.New("grpc down")})

	_, err := vs.ListCollections(context.Background(), "kb_ws_1_")
	if err == nil || !strings.Contains(err.Error(), "failed to list collections") {
		t.Fatalf("err = %v, want wrapped list error", err)
	}
}
