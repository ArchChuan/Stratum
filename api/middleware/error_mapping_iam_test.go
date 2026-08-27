package middleware

import (
	"net/http"
	"testing"

	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
)

func TestMapErrorToStatusIAMAdminUserNotFound(t *testing.T) {
	// 平台管理员管理对未知 user_id 的写操作（Set/RemoveAdminRole）返回
	// ErrUserNotFound，必须映射为 404 而非默认 500，与 ErrTenantNotFound 等一致。
	if got := MapErrorToStatus(iamdomain.ErrUserNotFound); got != http.StatusNotFound {
		t.Fatalf("ErrUserNotFound status = %d, want %d", got, http.StatusNotFound)
	}
	if got := MapErrorToStatus(iamdomain.ErrForbidden); got != http.StatusForbidden {
		t.Fatalf("ErrForbidden status = %d, want %d", got, http.StatusForbidden)
	}
}
