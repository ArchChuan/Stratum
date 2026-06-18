package port

// DocumentParser is the consumer-side port for document → text extraction.
// Concrete implementation lives in internal/knowledge/infrastructure/document.
type DocumentParser interface {
	ParseBytes(data []byte, hint string) (string, error)
}
