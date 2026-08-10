package gen

type EvaluationCenterQuery struct {
	ResourceKind string `form:"resource_kind" binding:"omitempty,oneof=skill agent mcp knowledge"`
	ResourceID   string `form:"resource_id"`
	Status       string `form:"status"`
	Cursor       string `form:"cursor"`
	Limit        int    `form:"limit" binding:"omitempty,min=1,max=100"`
}
