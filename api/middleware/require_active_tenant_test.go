package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestRequireActiveTenantFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		tenantID   string
		status     string
		queryErr   error
		wantStatus int
	}{
		{name: "missing context", wantStatus: http.StatusUnauthorized},
		{name: "missing tenant", tenantID: "tenant-1", queryErr: pgx.ErrNoRows, wantStatus: http.StatusForbidden},
		{name: "inactive", tenantID: "tenant-1", status: "disabled", wantStatus: http.StatusForbidden},
		{name: "dependency failure", tenantID: "tenant-1", queryErr: errors.New("db unavailable"), wantStatus: http.StatusServiceUnavailable},
		{name: "active", tenantID: "tenant-1", status: "active", wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if tc.tenantID != "" {
				query := pool.ExpectQuery("SELECT status FROM public.tenants").WithArgs(tc.tenantID)
				if tc.queryErr != nil {
					query.WillReturnError(tc.queryErr)
				} else {
					query.WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow(tc.status))
				}
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.tenantID != "" {
					c.Set("auth.tenant_id", tc.tenantID)
				}
				c.Next()
			})
			r.Use(requireActiveTenant(pool))
			r.POST("/write", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/write", nil)) //nolint:noctx
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
