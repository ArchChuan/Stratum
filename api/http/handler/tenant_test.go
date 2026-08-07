package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
)

// TestTenantIDFromCtx verifies tenantIDFromCtx extracts the tenant from the
// request context and fails closed (empty, false) when it is missing or blank.
func TestTenantIDFromCtx(t *testing.T) {
	valid := []struct {
		name string
		got  string
		want string
	}{
		{name: "present", got: "tenant-abc", want: "tenant-abc"},
		{name: "numeric", got: "20260807", want: "20260807"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			ctx := reqctx.WithTenantID(t.Context(), tc.got)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx) //nolint:noctx
			id, ok := tenantIDFromCtx(c)
			if !ok || id != tc.want {
				t.Fatalf("tenantIDFromCtx() = %q, %v; want %q, true", id, ok, tc.want)
			}
		})
	}

	t.Run("missing context fails closed", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(t.Context()) //nolint:noctx
		if id, ok := tenantIDFromCtx(c); ok {
			t.Fatalf("tenantIDFromCtx() = %q, true; want fail closed (empty, false)", id)
		}
	})
	t.Run("blank tenant fails closed", func(t *testing.T) {
		ctx := reqctx.WithTenantID(t.Context(), "")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx) //nolint:noctx
		if id, ok := tenantIDFromCtx(c); ok {
			t.Fatalf("tenantIDFromCtx() = %q, true; want fail closed (empty, false)", id)
		}
	})
}
