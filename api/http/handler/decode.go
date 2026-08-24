package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
)

// decodeClosedJSON decodes a request body rejecting unknown fields and
// trailing values, so exactly one well-formed JSON payload is required.
// 共享工具：供提案类 handler 复用（原定义于已删除的 system_assistant_handler.go）。
func decodeClosedJSON(c *gin.Context, dst any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode request body: multiple JSON values")
		}
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}
