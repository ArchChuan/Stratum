package opik

import "time"

type tracePage struct {
	Page    int         `json:"page"`
	Size    int         `json:"size"`
	Total   int64       `json:"total"`
	Content []opikTrace `json:"content"`
}

type spanPage struct {
	Page    int        `json:"page"`
	Size    int        `json:"size"`
	Total   int64      `json:"total"`
	Content []opikSpan `json:"content"`
}

type opikTrace struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	StartTime          time.Time      `json:"start_time"`
	EndTime            *time.Time     `json:"end_time"`
	Input              any            `json:"input"`
	Output             any            `json:"output"`
	Metadata           map[string]any `json:"metadata"`
	Usage              map[string]int `json:"usage"`
	ErrorInfo          *errorInfo     `json:"error_info"`
	TotalEstimatedCost float64        `json:"total_estimated_cost"`
	Duration           float64        `json:"duration"`
}

type opikSpan struct {
	ID                 string         `json:"id"`
	TraceID            string         `json:"trace_id"`
	ParentSpanID       string         `json:"parent_span_id"`
	Name               string         `json:"name"`
	Type               string         `json:"type"`
	StartTime          time.Time      `json:"start_time"`
	EndTime            *time.Time     `json:"end_time"`
	Input              any            `json:"input"`
	Output             any            `json:"output"`
	Metadata           map[string]any `json:"metadata"`
	Model              string         `json:"model"`
	Provider           string         `json:"provider"`
	Usage              map[string]int `json:"usage"`
	ErrorInfo          *errorInfo     `json:"error_info"`
	TotalEstimatedCost float64        `json:"total_estimated_cost"`
	Duration           float64        `json:"duration"`
}

type errorInfo struct {
	Message string `json:"message"`
	Type    string `json:"exception_type"`
}
