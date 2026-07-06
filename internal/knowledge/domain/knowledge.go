// Package domain holds knowledge context entities.
package domain

import "time"

type KB struct {
	ID, Name, Description, Collection string
}

type Document struct {
	ID, KBID, Source, ContentHash string
	IngestStatus                  string
	IngestError                   string
	ProcessedChunks               int
	TotalChunks                   int
	CreatedAt                     time.Time
	IngestStartedAt               *time.Time
	IngestFinishedAt              *time.Time
}

type Chunk struct {
	ID, DocID, Text string
	Index           int64
}
