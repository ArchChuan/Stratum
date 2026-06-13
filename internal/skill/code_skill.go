package skill

import (
	"context"
	"fmt"
)

// CodeSkill executes a user-supplied code snippet via CodeExecutor.
// Language must be "python" or "javascript".
type CodeSkill struct {
	*BaseSkill
	Code     string
	Language string
	executor *CodeExecutor
}

// NewCodeSkill creates a CodeSkill without an executor (placeholder; use NewCodeSkillWithExecutor).
func NewCodeSkill(id, name, description, code, language string) *CodeSkill {
	return &CodeSkill{
		BaseSkill: &BaseSkill{
			ID:          id,
			Name:        name,
			Description: description,
			Type:        "code",
		},
		Code:     code,
		Language: language,
	}
}

// NewCodeSkillWithExecutor creates a CodeSkill backed by the given CodeExecutor.
func NewCodeSkillWithExecutor(id, name, description, code, language string, exec *CodeExecutor) *CodeSkill {
	cs := NewCodeSkill(id, name, description, code, language)
	cs.executor = exec
	return cs
}

// Execute delegates to CodeExecutor if one is set, otherwise returns a placeholder.
// The input map may contain a "__tenant_id" key for per-tenant rate limiting.
func (cs *CodeSkill) Execute(ctx context.Context, input interface{}) (interface{}, error) {
	inputMap, ok := input.(map[string]interface{})
	if !ok {
		inputMap = map[string]interface{}{}
	}

	if cs.executor == nil {
		return map[string]interface{}{
			"code":     cs.Code,
			"language": cs.Language,
			"output":   "code executor not configured",
		}, nil
	}

	tenantID, _ := inputMap["__tenant_id"].(string)

	// Don't pass internal metadata to user code.
	userInput := make(map[string]interface{}, len(inputMap))
	for k, v := range inputMap {
		if k != "__tenant_id" {
			userInput[k] = v
		}
	}

	out, err := cs.executor.Execute(ctx, cs.Language, cs.Code, tenantID, userInput)
	if err != nil {
		return nil, fmt.Errorf("code skill %s: %w", cs.ID, err)
	}
	return out, nil
}
