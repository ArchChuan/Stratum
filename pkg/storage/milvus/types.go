package milvus

type DocumentChunk struct {
	ID             string
	UserID         string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Vector         []float32
}

type SearchResult struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Score          float32
}

type MCPRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type MCPResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}
