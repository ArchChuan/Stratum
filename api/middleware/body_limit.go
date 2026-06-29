package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// BodyLimit wraps the request body with http.MaxBytesReader to cap incoming
// payload size. Exceeding the limit causes ShouldBindJSON to return
// *http.MaxBytesError, which MapErrorToStatus maps to 413.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
