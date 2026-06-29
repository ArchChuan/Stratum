package executors

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

type PromptSkill struct {
	*domain.BaseSkill
	PromptTemplate string
}

func NewPromptSkill(id, name, description, promptTemplate string) *PromptSkill {
	return &PromptSkill{
		BaseSkill:      &domain.BaseSkill{ID: id, Name: name, Description: description, Type: "prompt"},
		PromptTemplate: promptTemplate,
	}
}

func (ps *PromptSkill) GetConfig() map[string]any {
	return map[string]any{"prompt_template": ps.PromptTemplate}
}

func (ps *PromptSkill) Execute(_ context.Context, input any) (any, error) {
	inputMap, _ := input.(map[string]any)
	userInput, _ := inputMap["prompt"].(string)

	tmpl, err := template.New("p").Parse(ps.PromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid prompt template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"input": userInput}); err != nil {
		return nil, fmt.Errorf("template render: %w", err)
	}
	return map[string]any{"content": buf.String()}, nil
}
