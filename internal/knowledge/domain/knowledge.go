// Package domain holds knowledge context entities.
package domain

type KB struct {
	ID, Name, Description, Collection string
}

type Document struct {
	ID, KBID, Source string
}

type Chunk struct {
	ID, DocID, Text string
	Index           int64
}
