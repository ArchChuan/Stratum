package dto

type SkillRequirements struct {
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	MemoryScopes          []string `json:"memoryScopes"`
}

type CreateSkillRequest struct {
	Name           string            `json:"name" binding:"required"`
	Goal           string            `json:"goal" binding:"required"`
	WhenToUse      string            `json:"whenToUse" binding:"required"`
	SampleInput    any               `json:"sampleInput"`
	ExpectedOutput any               `json:"expectedOutput"`
	Instructions   string            `json:"instructions" binding:"required"`
	Requirements   SkillRequirements `json:"requirements"`
}

type UpdateSkillCapabilityRequest struct {
	Goal       string `json:"goal"`
	WhenToUse  string `json:"whenToUse"`
	InputSpec  string `json:"inputSpec"`
	OutputSpec string `json:"outputSpec"`
}

type UpdateSkillActivationRequest struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
	Confirmed    bool           `json:"confirmed"`
}

type UpdateSkillInstructionBundleRequest struct {
	Instructions string            `json:"instructions"`
	Requirements SkillRequirements `json:"requirements"`
}

type SkillWorkspaceResponse struct {
	Skill SkillProductResponse  `json:"skill"`
	Draft SkillRevisionResponse `json:"draft"`
}

type SkillProductResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	ActiveRevisionID string `json:"activeRevisionId,omitempty"`
	DraftRevisionID  string `json:"draftRevisionId,omitempty"`
}

type SkillRevisionResponse struct {
	ID                 string         `json:"id"`
	SkillID            string         `json:"skillId"`
	RevisionNo         int            `json:"revisionNo,omitempty"`
	Status             string         `json:"status"`
	Capability         map[string]any `json:"capability"`
	ActivationContract map[string]any `json:"activationContract"`
	Instructions       string         `json:"instructions"`
	Requirements       map[string]any `json:"requirements"`
	PublishChecks      map[string]any `json:"publishChecks,omitempty"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
