package port

import (
	"errors"
	"testing"
)

func TestValidationErrorField(t *testing.T) {
	e := &ValidationError{Location: "facts", FieldName: "fact_type", Value: "bad", Reason: "invalid enum"}
	if got := e.Field(); got != "fact_type" {
		t.Fatalf("Field() = %q, want fact_type", got)
	}
}

// TestValidationErrorFieldNilSafe 验证 typed-nil 不 panic：内核 errors.As 命中
// FieldError 类型后调用 Field()，nil 接收者必须返回空串而非 panic。
func TestValidationErrorFieldNilSafe(t *testing.T) {
	var nilErr *ValidationError
	var wrapped error = nilErr // typed-nil 包进 error 接口
	var fe interface{ Field() string }
	if !errors.As(wrapped, &fe) {
		t.Fatal("errors.As must match typed-nil *ValidationError")
	}
	if got := fe.Field(); got != "" {
		t.Fatalf("typed-nil Field() = %q, want empty", got)
	}
	if got := (*ValidationError)(nil).Field(); got != "" {
		t.Fatalf("direct nil Field() = %q, want empty", got)
	}
}
