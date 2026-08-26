package middleware

import (
	"net/http"
	"testing"

	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// TestMapVersioningErrors 守卫通用版本基座的错误映射：回滚目标不存在或
// 非 deprecated 历史版本时返回 404（对齐 skill 回滚的 ErrSkillNotFound 语义）。
func TestMapVersioningErrors(t *testing.T) {
	if got := MapErrorToStatus(versioningdomain.ErrVersionNotFound); got != http.StatusNotFound {
		t.Errorf("MapErrorToStatus(ErrVersionNotFound)=%d want %d", got, http.StatusNotFound)
	}
}
