package milvus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeMilvusClient is a white-box stub for the SDK Client interface. The nil
// embedded *client.GrpcClient satisfies the interface; only the methods the
// path under test reaches are overridden, everything else would panic if
// touched (which itself signals an unexpected call).
type fakeMilvusClient struct {
	*client.GrpcClient
	loadErr   error
	queryErr  error
	searchErr error
}

func (f *fakeMilvusClient) LoadCollection(context.Context, string, bool, ...client.LoadCollectionOption) error {
	return f.loadErr
}

func (f *fakeMilvusClient) Query(_ context.Context, _ string, _ []string, _ string, _ []string, _ ...client.SearchQueryOptionFunc) (client.ResultSet, error) {
	return nil, f.queryErr
}

func (f *fakeMilvusClient) Search(_ context.Context, _ string, _ []string, _ string, _ []string, _ []entity.Vector, _ string, _ entity.MetricType, _ int, _ entity.SearchParam, _ ...client.SearchQueryOptionFunc) ([]client.SearchResult, error) {
	return nil, f.searchErr
}

func newStoreWithClient(fake *fakeMilvusClient) *VectorStore {
	vs := NewVectorStore("localhost", "19530", zap.NewNop())
	vs.mu.Lock()
	vs.client = fake
	vs.mu.Unlock()
	return vs
}

var errNotFound = status.Error(codes.InvalidArgument, "collection not found: missing_coll")

func TestIsCollectionNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "sentinel", err: ErrCollectionNotFound, want: true},
		{name: "milvus message", err: errNotFound, want: true},
		{name: "does not exist", err: status.Error(codes.InvalidArgument, "collection missing does not exist"), want: true},
		// "index not found" means the collection exists but cannot be loaded:
		// that must surface as a real error, not masquerade as empty data.
		{name: "index not found is not missing collection", err: status.Error(codes.Internal, "index not found: coll"), want: false},
		{name: "unrelated failure", err: errors.New("connection reset"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCollectionNotFound(tt.err); got != tt.want {
				t.Fatalf("isCollectionNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsDimensionMismatch(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "sentinel", err: ErrDimensionMismatch, want: true},
		// Milvus 以 InvalidArgument + 以下消息形态暴露 dim mismatch。
		{name: "dimension mismatch message", err: status.Error(codes.InvalidArgument, "dimension mismatch: query vector dimension (1024) does not match collection dimension (1536)"), want: true},
		{name: "does not match message", err: status.Error(codes.InvalidArgument, "query vector dimension (2) doesn't match collection dimension (1024)"), want: true},
		{name: "wrapped sentinel", err: fmt.Errorf("failed to search vectors: %w", ErrDimensionMismatch), want: true},
		// 真 outage 不得被误分类。
		{name: "unavailable", err: status.Error(codes.Unavailable, "connection refused"), want: false},
		{name: "unrelated failure", err: errors.New("collection not found: missing"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDimensionMismatch(tt.err); got != tt.want {
				t.Fatalf("isDimensionMismatch(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestSearchWithFilter_DimensionMismatchIsClassifiedNotOutage(t *testing.T) {
	// 查询向量维度与 collection 维度不一致（模型切换后的存量集合）是确定性
	// 数据形态错误，不是向量库 outage：必须翻译为 ErrDimensionMismatch，
	// 让调用方降级跳过，而不是按通用搜索失败告警。
	for _, searchErr := range []error{
		status.Error(codes.InvalidArgument, "dimension mismatch: query vector dimension (1024) does not match collection dimension (1536)"),
		status.Error(codes.InvalidArgument, "query vector dimension (2) doesn't match collection dimension (1024)"),
	} {
		vs := newStoreWithClient(&fakeMilvusClient{searchErr: searchErr})
		results, err := vs.SearchWithFilter(context.Background(), "coll", []float32{0.5}, 5, "")
		if results != nil || !errors.Is(err, ErrDimensionMismatch) {
			t.Fatalf("search with dim mismatch = (%v, %v), want (nil, ErrDimensionMismatch)", results, err)
		}
	}
}

func TestSearchWithFilter_CollectionNotFoundReturnsEmpty(t *testing.T) {
	// Memory path: collections are provisioned lazily, so a missing collection
	// before first use is a legal empty result, not a failure.
	vs := newStoreWithClient(&fakeMilvusClient{loadErr: errNotFound})
	results, err := vs.SearchWithFilter(context.Background(), "memory_t", []float32{0.5}, 5, "")
	if err != nil {
		t.Fatalf("search with missing collection: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestSearchWithFilterStrict_CollectionNotFoundFailsClosed(t *testing.T) {
	// RAG retrieval: a missing collection is distinguishable (drift vs
	// legitimately empty workspace is the caller's decision), never an
	// implicit empty result.
	vs := newStoreWithClient(&fakeMilvusClient{loadErr: errNotFound})
	results, err := vs.SearchWithFilterStrict(context.Background(), "tenant_t_kb", []float32{0.5}, 5, "")
	if results != nil || !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("strict search = (%v, %v), want (nil, ErrCollectionNotFound)", results, err)
	}
}

func TestSearchWithFilter_LoadFailurePropagates(t *testing.T) {
	// A non-missing failure (Milvus down, index missing) must not be
	// collapsed into an empty result.
	for _, loadErr := range []error{
		errors.New("connection refused"),
		status.Error(codes.Internal, "index not found: coll"),
	} {
		vs := newStoreWithClient(&fakeMilvusClient{loadErr: loadErr})
		results, err := vs.SearchWithFilter(context.Background(), "coll", []float32{0.5}, 5, "")
		if results != nil || err == nil {
			t.Fatalf("search = (%v, %v), want (nil, error)", results, err)
		}
		if errors.Is(err, ErrCollectionNotFound) {
			t.Fatalf("load error %v misclassified as collection not found", loadErr)
		}
	}
}

func TestCountVectors_CollectionNotFoundFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		loadErr  error
		queryErr error
	}{
		{name: "load phase", loadErr: errNotFound},
		{name: "query phase", queryErr: errNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := newStoreWithClient(&fakeMilvusClient{loadErr: tt.loadErr, queryErr: tt.queryErr})
			n, err := vs.CountVectors(context.Background(), "missing_coll", "")
			if n != 0 || !errors.Is(err, ErrCollectionNotFound) {
				t.Fatalf("CountVectors = (%d, %v), want (0, ErrCollectionNotFound)", n, err)
			}
		})
	}
}

func TestKeywordSearch_CollectionNotFoundFailsClosed(t *testing.T) {
	vs := newStoreWithClient(&fakeMilvusClient{loadErr: errNotFound})
	results, err := vs.KeywordSearch(context.Background(), "missing_coll", "query", 5)
	if results != nil || !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("KeywordSearch = (%v, %v), want (nil, ErrCollectionNotFound)", results, err)
	}
}
