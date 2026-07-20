package application

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestApplicationDoesNotImportStorageImplementations(t *testing.T) {
	files := []string{"ingest_service.go", "rag_service.go", "mocks.go"}
	forbidden := map[string]struct{}{
		"github.com/byteBuilderX/stratum/pkg/vector":           {},
		"github.com/byteBuilderX/stratum/pkg/storage/postgres": {},
		"github.com/byteBuilderX/stratum/pkg/textchunk":        {},
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
