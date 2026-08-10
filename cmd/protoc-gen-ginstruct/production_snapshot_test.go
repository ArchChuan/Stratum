package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// updateGolden 记录基线:go test -update 时把当前生成器输出写回 golden。
var updateGolden = flag.Bool("update", false, "rewrite production snapshot goldens from current generator output")

// productionProtos 登记全部已迁移的生产 proto(proto/ 相对路径)。每个文件
// 生成的 Go struct 文本必须与 testdata/production-snapshots/ 下的批准基线
// 逐字节一致:生成器静默漂移导致 wire 形状变化(字段名、json tag、binding、
// Go 类型)时本测试直接变红,补上 parity 终态 gen-vs-gen 自对照(Task 19 后
// 手写 dto 包消亡)的空洞。基线入 git,fresh checkout 无需 make proto-gen
// 即可运行。
var productionProtos = []string{
	"collaboration/collaboration.proto",
	"agent/agent.proto",
	"agent/operation_proposal.proto",
	"agent/resource_change_proposal.proto",
	"mcp/mcp_config.proto",
	"workflow/workflow.proto",
	"evaluation/evaluation.proto",
	"scheduler/scheduled_task.proto",
	"admin/admin.proto",
	"dashboard/dashboard.proto",
	"memory/memory.proto",
	"skill/skill.proto",
	"knowledge/rag.proto",
}

// snapshotGolden maps a proto relative path to its checked-in Go golden.
// 基线记录:生成器行为有意变更时,在包目录执行
//
//	go test ./cmd/protoc-gen-ginstruct -run TestProductionProtoSnapshots -update
//
// 重写 golden 并与 api/http/dto/gen/ 磁盘产物对账后再提交。
func snapshotGolden(protoRel string) string {
	return filepath.Join("testdata", "production-snapshots",
		filepath.Base(protoRel)+".golden.go")
}

// TestProductionProtoSnapshots 逐文件字节级快照:用真实 protocompile 编译
// proto/ 目录下的生产 proto(SourceInfoStandard 保 @binding/@gotype/@omitempty
// 注释),把生成器 Go 输出与 testdata/production-snapshots/ 基线逐字节比较。
// -update 时把当前输出写回基线(记录新基线后必须人工对账磁盘产物)。
func TestProductionProtoSnapshots(t *testing.T) {
	for _, name := range productionProtos {
		t.Run(name, func(t *testing.T) {
			req := buildProductionRequest(t, name)
			resp := generate(req)
			if resp.Error != nil {
				t.Fatalf("generate: %s", *resp.Error)
			}
			var got string
			for _, f := range resp.File {
				if f.GetName() == goFileName(name) {
					got = f.GetContent()
				}
			}
			if got == "" {
				t.Fatalf("response missing Go file %q", goFileName(name))
			}
			golden := snapshotGolden(name)
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatalf("mkdir golden dir: %v", err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden %s: %v", golden, err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden %s (run with -update to record baseline): %v", golden, err)
			}
			if got != string(want) {
				t.Errorf("%s differs from golden %s:\n%s", goFileName(name), golden,
					diff(got, string(want)))
			}
		})
	}
}

// buildProductionRequest 以 proto/ 为 import path 编译生产 proto,使规范
// Path() 为 "agent/agent.proto" 形态——与 buf generate(inputs directory: proto)
// 的路径一致,生成的 "// source: ..." 注释才能与磁盘产物逐字节对齐。
func buildProductionRequest(t *testing.T, protoRel string) *pluginpb.CodeGeneratorRequest {
	t.Helper()
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{"../../proto"},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(context.Background(), protoRel)
	if err != nil {
		t.Fatalf("compile %s: %v", protoRel, err)
	}
	descriptors := make([]*descriptorpb.FileDescriptorProto, 0, len(files))
	names := make([]string, 0, len(files))
	for _, f := range files {
		// v0.14.1: linker.File embeds protoreflect.FileDescriptor (no Proto());
		// the descriptor accessor lives on linker.Result — same as buildRequest.
		fd, ok := f.(linker.Result)
		if !ok {
			t.Fatalf("compile result %T does not implement linker.Result", f)
		}
		descriptors = append(descriptors, fd.FileDescriptorProto())
		names = append(names, f.Path())
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: names,
		ProtoFile:      descriptors,
	}
}
