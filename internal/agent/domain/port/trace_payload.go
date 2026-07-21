package port

import "context"

type TracePayload struct {
	TenantID string
	TraceID  string
	Kind     string
	Value    any
}

type TracePayloadRef struct {
	Reference string
	SHA256    string
	SizeBytes int64
}

type TracePayloadStore interface {
	Put(context.Context, TracePayload) (TracePayloadRef, error)
}
