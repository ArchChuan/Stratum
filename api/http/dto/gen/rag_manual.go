package gen

import "mime/multipart"

// UploadDocumentRequest is bound from POST /knowledge/ingest multipart form.
type UploadDocumentRequest struct {
	Workspace string                `form:"workspace" binding:"required"`
	File      *multipart.FileHeader `form:"file" binding:"required"`
	// AllowedUserIDs/AllowedRoleIDs form the document-level access whitelist;
	// sent as repeated multipart form fields (allowed_user_ids=a&allowed_user_ids=b).
	// Empty => unrestricted. Ignored for platform-managed workspaces.
	AllowedUserIDs []string `form:"allowed_user_ids"`
	AllowedRoleIDs []string `form:"allowed_role_ids"`
}
