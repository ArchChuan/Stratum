package gen

import "mime/multipart"

// UploadDocumentRequest is bound from POST /knowledge/ingest multipart form.
type UploadDocumentRequest struct {
	Workspace string                `form:"workspace" binding:"required"`
	File      *multipart.FileHeader `form:"file" binding:"required"`
}
