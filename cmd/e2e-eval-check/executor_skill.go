package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// skillExecutor evaluates a skill by executing it through a temporary carrier
// agent whose tool set includes the skill, then judging the output.
type skillExecutor struct {
	client *httpClient // injected by tests to bypass token minting
}

func init() {
	registerExecutor("skill", func() executor { return &skillExecutor{} })
}

func (e *skillExecutor) Execute(ctx context.Context, o options, p point) (execResult, error) {
	client := e.client
	if client == nil {
		token, err := ownerTokenFor(o)
		if err != nil {
			return execResult{}, err
		}
		client = newHTTPClient(o.baseURL, token)
	}
	agentID, err := createCarrierAgent(ctx, client, p)
	if err != nil {
		return execResult{}, err
	}
	inner := &llmExecutor{agents: &agentClient{client: client}, agentID: agentID}
	if p.Judge != nil {
		inner.judge = newJudgeClient(p.Judge, apiKeyFromEnv(p.Judge.APIKeyEnv))
	}
	dataset, err := loadLLMSet(p)
	if err != nil {
		return execResult{}, err
	}
	res, err := inner.runCases(ctx, dataset)
	if err != nil {
		return res, err
	}
	// best-effort cleanup of the transient carrier agent; failure surfaces as residual.
	if delErr := client.deleteAgent(ctx, agentID); delErr != nil {
		res.Residuals = append(res.Residuals, agentID)
	}
	res.Evidence = append(res.Evidence, evidence{Kind: "carrier_agent", Ref: agentID})
	return res, nil
}

// createCarrierAgent provisions a temporary agent from the point's snapshot.
// The wire payload matches the handler-bound CreateAgentRequest (camelCase
// systemPrompt/llmModel/maxIterations/allowedSkills plus required memoryScope),
// not the proto-gen DTO. The proto-gen CreateAgentRequest is generated but the
// agent_crud_handler binds the hand-written camelCase DTO in agent_dto.go.
//
//	snapshot:
//	  skill:
//	    name: my-skill
//	    description: "..."
//	    content: "..."
//	  agent:
//	    model: qwen-plus
//	    system_prompt: "..."
func createCarrierAgent(ctx context.Context, client *httpClient, p point) (string, error) {
	skill, ok := p.Snapshot["skill"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("skill point snapshot.skill required")
	}
	agentCfg, _ := p.Snapshot["agent"].(map[string]any)
	skillName, _ := skill["name"].(string)
	description, _ := skill["description"].(string)
	payload := map[string]any{
		"name":          "eval-carrier-" + p.Key,
		"description":   description,
		"systemPrompt":  agentCfg["system_prompt"],
		"llmModel":      agentCfg["model"],
		"maxIterations": DefaultCarrierAgentMaxIterations,
		"allowedSkills": []string{skillName},
		"memoryScope":   DefaultCarrierAgentMemoryScope,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode create agent: %w", err)
	}
	status, data, err := client.roundtrip(ctx, http.MethodPost, "/agents", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if err := classifyHTTP("/agents", status, string(data)); err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
		return "", &infraError{fmt.Errorf("create agent response missing id")}
	}
	return created.ID, nil
}
