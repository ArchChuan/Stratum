package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestErrorHandlerRendersWorkflowInputIssues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(zap.NewNop()))
	router.GET("/", func(c *gin.Context) {
		_ = c.Error(&workflowdomain.InputValidationError{Issues: []workflowdomain.InputIssue{{
			Field: "region", Code: "invalid", Message: "区域格式不正确",
		}}})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.JSONEq(t, `{"error":"workflow input validation failed","issues":[{"field":"region","code":"invalid","message":"区域格式不正确"}]}`, recorder.Body.String())
}

func TestErrorHandlerRendersWorkflowGraphIssues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(zap.NewNop()))
	router.GET("/", func(c *gin.Context) {
		_ = c.Error(&workflowdomain.GraphValidationError{Issues: []workflowdomain.GraphIssue{{
			Path: "graph", Code: "invalid", Message: "至少需要一个节点",
		}}})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.JSONEq(t, `{"error":"workflow graph validation failed","issues":[{"path":"graph","code":"invalid","message":"至少需要一个节点"}]}`, recorder.Body.String())
}
