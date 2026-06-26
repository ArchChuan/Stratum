package constants

import (
	"fmt"
	"regexp"
)

const (
	// MaxUploadFileSize is the maximum file size for document uploads (100MB).
	MaxUploadFileSize = 100 * 1024 * 1024

	// CollectionPrefix is the unified prefix for all knowledge workspace collections.
	CollectionPrefix = "kb"
)

var milvusUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// CollectionName generates the Milvus collection name for a knowledge workspace.
// workspaceID must be the stable workspace ID, not the mutable name.
// CollectionName returns the Milvus collection name for a workspace.
// workspaceID (UUID v7) is globally unique, so tenantID is ignored.
func CollectionName(_, workspaceID string) string {
	san := func(s string) string { return milvusUnsafe.ReplaceAllString(s, "_") }
	return fmt.Sprintf("%s_%s", CollectionPrefix, san(workspaceID))
}
