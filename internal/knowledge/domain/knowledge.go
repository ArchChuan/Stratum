// Package domain holds knowledge context entities.
package domain

import "time"

type KB struct {
	ID, Name, Description, Collection string
}

type Document struct {
	ID, KBID, Source, ContentHash string
	// Title is the display title. Falls back to Source for legacy rows where
	// the title column was populated with the filename.
	Title string
	// Metadata carries structured document attributes (e.g.
	// {"builtin_source":"docs/knowledge/guides/agent.md"} for built-in docs).
	// Empty map => unrestricted/no attributes. Never nil after a repo read.
	Metadata         map[string]any
	IngestStatus     string
	IngestError      string
	ProcessedChunks  int
	TotalChunks      int
	CreatedAt        time.Time
	IngestStartedAt  *time.Time
	IngestFinishedAt *time.Time

	// AllowedUserIDs/AllowedRoleIDs form the document-level access whitelist.
	// Both empty => unrestricted (inherits workspace visibility); either
	// non-empty => viewer visible iff in user whitelist, in role whitelist,
	// or the document creator (CreatedBy).
	AllowedUserIDs []string
	AllowedRoleIDs []string
	// CreatedBy is the uploading user; creator is implicitly allowed to see
	// the document (never locks themselves out). Empty for legacy rows.
	CreatedBy string
}

type Chunk struct {
	ID, DocID, Text string
	Index           int64
	ParentID        string // set when using Parent-Child chunking strategy; references parent chunk ID
}
