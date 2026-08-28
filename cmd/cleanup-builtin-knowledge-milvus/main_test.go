package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

// stubDropper 注入 ListCollections / DeleteCollection,验证 cleanup 的编排而不连真 Milvus。
type stubDropper struct {
	cols      []string
	deleted   []string
	listErr   error
	deleteErr map[string]error
}

// ListCollections 复刻 VectorStore 的前缀过滤语义:只返回 HasPrefix(prefix) 的集合。
func (s *stubDropper) ListCollections(_ context.Context, prefix string) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]string, 0, len(s.cols))
	for _, c := range s.cols {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *stubDropper) DeleteCollection(_ context.Context, name string) error {
	if err := s.deleteErr[name]; err != nil {
		return err
	}
	s.deleted = append(s.deleted, name)
	return nil
}

func TestCleanup(t *testing.T) {
	legacy := "kb_a0a0a0a0_0000_0000_0000_000000000001"
	cases := []struct {
		name     string
		cols     []string
		dryRun   bool
		listErr  error
		deleteBy map[string]error
		wantAff  []string
		wantDel  []string
		wantErr  bool
	}{
		{
			name:    "dry run lists without deleting",
			cols:    []string{legacy, legacy + "_bge-m3"},
			dryRun:  true,
			wantAff: []string{legacy, legacy + "_bge-m3"},
			wantDel: nil,
		},
		{
			name:    "execute deletes legacy and model-suffixed collections",
			cols:    []string{legacy, legacy + "_bge-m3", legacy + "_text-embedding-v3"},
			wantAff: []string{legacy, legacy + "_bge-m3", legacy + "_text-embedding-v3"},
			wantDel: []string{legacy, legacy + "_bge-m3", legacy + "_text-embedding-v3"},
		},
		{
			name:    "no matching collections returns empty",
			cols:    []string{"kb_other_workspace", "memory_abc"},
			wantAff: []string{},
			wantDel: nil,
		},
		{
			name:    "list error propagates",
			cols:    []string{legacy},
			listErr: errors.New("milvus down"),
			wantErr: true,
		},
		{
			name:     "delete error propagates and stops",
			cols:     []string{legacy, legacy + "_m"},
			deleteBy: map[string]error{legacy: errors.New("drop failed")},
			wantErr:  true,
		},
		{
			name:     "collection not found is tolerated (idempotent)",
			cols:     []string{legacy, legacy + "_m"},
			deleteBy: map[string]error{legacy: milvus.ErrCollectionNotFound},
			wantAff:  []string{legacy, legacy + "_m"},
			wantDel:  []string{legacy + "_m"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubDropper{cols: tc.cols, listErr: tc.listErr, deleteErr: tc.deleteBy}
			affected, err := cleanup(context.Background(), stub, tc.dryRun)
			if (err != nil) != tc.wantErr {
				t.Fatalf("cleanup error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !equalStrings(affected, tc.wantAff) {
				t.Fatalf("affected = %v, want %v", affected, tc.wantAff)
			}
			if !equalStrings(stub.deleted, tc.wantDel) {
				t.Fatalf("deleted = %v, want %v", stub.deleted, tc.wantDel)
			}
		})
	}
}

func TestCleanupLegacyPrefix(t *testing.T) {
	// 锚点:清理前缀必须与 production 命名函数 CollectionLegacyName 一致,
	// 若命名漂移(如 SanitizeMilvusName 语义变化)此处即失败,脚本清理目标失准。
	want := "kb_a0a0a0a0_0000_0000_0000_000000000001"
	if got := constants.CollectionLegacyName("", builtinWorkspaceID); got != want {
		t.Fatalf("builtin legacy prefix = %q, want %q", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
