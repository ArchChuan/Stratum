package wiring

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/parameters/domain"
)

type fakeModelDirectory struct {
	chat      []string
	embedding []string
}

func (f *fakeModelDirectory) ListChatModelsByTenant(context.Context) ([]string, error) {
	return f.chat, nil
}

func (f *fakeModelDirectory) ListEmbeddingModelsByTenant(context.Context) ([]string, error) {
	return f.embedding, nil
}

// TestValidateEmbeddingModelInDirectory 验证 embedding 参数走 embedding 目录
// 校验（chat 模型被拒、空串哨兵放行），且 chat 参数链不受影响。
func TestValidateEmbeddingModelInDirectory(t *testing.T) {
	dir := &fakeModelDirectory{
		chat:      []string{"glm-4-flash"},
		embedding: []string{"embedding-3"},
	}
	validate := validateEmbeddingModelInDirectory(dir, "memory.embedding_model")
	if err := validate("embedding-3"); err != nil {
		t.Fatalf("embedding-3 should pass: %v", err)
	}
	if err := validate("glm-4-flash"); err == nil {
		t.Fatal("chat model should be rejected for embedding param")
	}
	if err := validate(""); err != nil {
		t.Fatalf("empty unset sentinel should pass: %v", err)
	}
	if err := validate("nope"); err == nil {
		t.Fatal("unknown model should be rejected")
	}
}

// TestInjectEmbeddingModelValidation 验证 injectModelDirectoryValidation 把
// ValidateFn 挂到 memory.embedding_model（registry 存在即可，无需真实目录）。
func TestInjectEmbeddingModelValidation(t *testing.T) {
	c := &Container{
		Parameters: &Parameters{Registry: domain.NewParametersRegistry()},
	}
	c.injectModelDirectoryValidation()
	def, ok := c.Parameters.Registry.Get("memory.embedding_model")
	if !ok {
		t.Fatal("memory.embedding_model not registered")
	}
	if def.ValidateFn == nil {
		t.Fatal("memory.embedding_model ValidateFn not injected")
	}
	if err := def.ValidateFn(""); err != nil {
		t.Fatalf("unset sentinel should pass: %v", err)
	}
}
