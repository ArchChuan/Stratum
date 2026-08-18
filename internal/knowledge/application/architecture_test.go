package application

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestApplicationDoesNotImportStorageImplementations(t *testing.T) {
	// 白名单随新文件扩展：application 层新增文件必须在此登记，防止
	// 绕过架构约束的 import 溜进来。
	files := []string{"ingest_service.go", "rag_service.go", "mocks.go", "no_answer.go", "evidence_gate.go"}
	forbidden := map[string]struct{}{
		"github.com/byteBuilderX/stratum/pkg/vector":           {},
		"github.com/byteBuilderX/stratum/pkg/storage/postgres": {},
		"github.com/byteBuilderX/stratum/pkg/textchunk":        {},
		// knowledge 零 LLM 依赖约束：judge 接口在 domain/port 消费，
		// 实现只在组合根（api/wiring），application 禁止触碰 llmgateway。
		"github.com/byteBuilderX/stratum/internal/llmgateway/domain":         {},
		"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure": {},
	}

	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", file, err)
			}
			if _, blocked := forbidden[path]; blocked {
				t.Errorf("%s imports storage implementation %s", file, path)
			}
		}
	}
}
