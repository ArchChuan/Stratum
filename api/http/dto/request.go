package dto

type CreateSkillRequest struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	Goal           string `json:"goal"`
	WhenToUse      string `json:"whenToUse"`
	SampleInput    any    `json:"sampleInput"`
	Type           string `json:"type" binding:"omitempty,oneof=code llm http prompt"`
	ExpectedInput  string `json:"expectedInput"`
	ExpectedOutput string `json:"expectedOutput"`
	SampleCases    string `json:"sampleCases"`
	// code
	Code     string `json:"code"`
	Language string `json:"language" binding:"omitempty,oneof=python javascript"`
	// llm
	SystemPrompt string  `json:"systemPrompt"`
	Model        string  `json:"model"`
	Temperature  float32 `json:"temperature"`
	MaxTokens    int     `json:"maxTokens"`
	// http
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"bodyTemplate"`
	TimeoutSec   int               `json:"timeoutSec"`
	// prompt
	PromptTemplate string `json:"promptTemplate"`
}

type SkillResponse struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Type           string         `json:"type"`
	Config         map[string]any `json:"config,omitempty"`
	AnalysisErrors []string       `json:"analysis_errors,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ExecuteSkillRequest struct {
	Input   interface{} `json:"input"`
	TraceID string      `json:"traceID"`
}

type ExecuteDraftSkillRequest struct {
	Skill   CreateSkillRequest `json:"skill" binding:"required"`
	Input   interface{}        `json:"input"`
	TraceID string             `json:"traceID"`
}

type UpdateSkillCapabilityRequest struct {
	Goal       string `json:"goal"`
	WhenToUse  string `json:"whenToUse"`
	InputSpec  string `json:"inputSpec"`
	OutputSpec string `json:"outputSpec"`
}

type UpdateSkillContractRequest struct {
	ToolName        string         `json:"toolName"`
	Description     string         `json:"description"`
	InputSchema     map[string]any `json:"inputSchema"`
	OutputSchema    map[string]any `json:"outputSchema"`
	CallingGuidance string         `json:"callingGuidance"`
	Confirmed       bool           `json:"confirmed"`
}

type UpdateSkillImplementationRequest struct {
	Mode        string         `json:"mode"`
	Source      map[string]any `json:"source"`
	Runtime     map[string]any `json:"runtime"`
	Permissions map[string]any `json:"permissions"`
	SecretRefs  []string       `json:"secretRefs"`
}

type ExecuteSkillResponse struct {
	Result     interface{} `json:"result"`
	Error      string      `json:"error,omitempty"`
	TraceID    string      `json:"traceID,omitempty"`
	DurationMs int64       `json:"durationMs,omitempty"`
}

type SkillWorkspaceResponse struct {
	Skill SkillProductResponse `json:"skill"`
	Draft SkillVersionResponse `json:"draft"`
}

type SkillProductResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	ActiveVersionID string `json:"activeVersionId,omitempty"`
	DraftVersionID  string `json:"draftVersionId,omitempty"`
}

type SkillVersionResponse struct {
	ID             string         `json:"id"`
	SkillID        string         `json:"skillId"`
	VersionNo      int            `json:"versionNo,omitempty"`
	Status         string         `json:"status"`
	Capability     map[string]any `json:"capability"`
	ToolContract   map[string]any `json:"toolContract"`
	Implementation map[string]any `json:"implementation"`
	TestBaseline   map[string]any `json:"testBaseline,omitempty"`
	PublishChecks  map[string]any `json:"publishChecks,omitempty"`
}
