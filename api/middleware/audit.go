package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/safetext"
	"github.com/gin-gonic/gin"
)

// maxAuditBodyBytes caps the request body captured in audit before/after snapshots.
const maxAuditBodyBytes = 8192

// authPathPrefix marks routes whose bodies carry credentials by design
// (login, register, guest, refresh); their bodies are not audited at all.
const authPathPrefix = "/auth/"

// AuditMiddleware records non-GET mutating requests to the audit log.
// It captures the authenticated actor from JWT claims, the request body
// as the "after" snapshot, and delegates async persistence to the recorder.
func AuditMiddleware(recorder auditport.AuditRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		actor := extractAuditActor(c)
		body := auditBodySnapshot(c)

		event := domain.AuditEvent{
			TenantID:     tenantIDFromContext(c),
			Actor:        actor,
			Action:       c.Request.Method + " " + c.FullPath(),
			ResourceType: "http_request",
			ResourceID:   c.Request.URL.Path,
			After:        body,
			RequestID:    requestIDFromContext(c),
			TraceID:      traceIDFromContext(c),
			RiskLevel:    auditRiskLevel(c.Request.Method),
			Outcome:      "success",
			OccurredAt:   time.Now(),
		}

		c.Next()

		// Override outcome if the handler set an error status.
		if c.Writer.Status() >= 400 {
			event.Outcome = "error"
		}
		_ = recorder.Record(c.Request.Context(), event)
	}
}

func extractAuditActor(c *gin.Context) domain.AuditActor {
	sub, _ := c.Get("auth.sub")
	if sub == nil {
		return domain.AuditActor{ActorType: domain.ActorTypeSystem, ActorID: "anonymous"}
	}
	return domain.AuditActor{ActorType: domain.ActorTypeUser, ActorID: sub.(string)}
}

func tenantIDFromContext(c *gin.Context) string {
	tid, _ := c.Get("auth.tenant_id")
	if tid == nil {
		return ""
	}
	return tid.(string)
}

func requestIDFromContext(c *gin.Context) string {
	rid, _ := c.Get("request_id")
	if rid == nil {
		return ""
	}
	return rid.(string)
}

func traceIDFromContext(c *gin.Context) string {
	tid, _ := c.Get("trace_id")
	if tid == nil {
		return ""
	}
	return tid.(string)
}

// auditBodySnapshot reads the request body, redacts credentials from it, and
// restores the original body for downstream handlers. Bodies of auth routes
// are dropped entirely: they carry credentials by design (login, register,
// guest) and have low audit value. The redacted snapshot is the only form
// that ever leaves this function — never log or persist the raw body.
func auditBodySnapshot(c *gin.Context) []byte {
	if strings.HasPrefix(c.Request.URL.Path, authPathPrefix) {
		// Drain the body so the downstream handler still sees it.
		readBodySnapshot(c)
		return nil
	}
	body := readBodySnapshot(c)
	if len(body) == 0 {
		return nil
	}
	return []byte(safetext.RedactCredentials(string(body)))
}

func readBodySnapshot(c *gin.Context) []byte {
	if c.Request.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxAuditBodyBytes))
	if err != nil {
		return nil
	}
	// Restore body for downstream handlers.
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	if len(body) == 0 {
		return nil
	}
	return body
}

func auditRiskLevel(method string) string {
	switch method {
	case "DELETE":
		return "high"
	case "POST", "PUT", "PATCH":
		return "medium"
	default:
		return "low"
	}
}
