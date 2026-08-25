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

func (e *skillExecutor) Execute(ctx context.Context, o options, p point) (res execResult, err error) {
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
	// Best-effort cleanup of the transient carrier agent must run on every exit
	// path (dataset load error, run error, success), not just success — an early
	// return would leak orphan agents invisibly across soak runs. A failed
	// DELETE surfaces the agent id as a residual for the report.
	defer func() {
		if delErr := client.deleteAgent(ctx, agentID); delErr != nil {
			res.Residuals = append(res.Residuals, agentID)
		}
		res.Evidence = append(res.Evidence, evidence{Kind: "carrier_agent", Ref: agentID})
	}()
	inner := &llmExecutor{agents: &agentClient{client: client}, agentID: agentID}
	if p.Judge != nil {
		inner.judge = newJudgeClient(p.Judge, apiKeyFromEnv(p.Judge.APIKeyEnv))
	}
	dataset, err := loadLLMSet(p)
	if err != nil {
		return res, err
	}
	return inner.runCases(ctx, dataset)
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
//
// carrierAgentSpec carries the validated snapshot fields needed to provision a
// carrier agent. Parsing lives in parseCarrierAgentSpec so createCarrierAgent
// stays a thin payload builder and the fail-closed guards stay testable.
type carrierAgentSpec struct {
	skillName    string
	description  string
	model        string
	systemPrompt any
}

// parseCarrierAgentSpec fails closed on dataset defects that would otherwise
// produce a carrier agent without the skill (empty allowedSkills entry) or a
// payload the server rejects with a generic binding 400 (null llmModel).
func parseCarrierAgentSpec(p point) (carrierAgentSpec, error) {
	skill, ok := p.Snapshot["skill"].(map[string]any)
	if !ok {
		return carrierAgentSpec{}, fmt.Errorf("skill point snapshot.skill required")
	}
	skillName, ok := skill["name"].(string)
	if !ok || skillName == "" {
		// An empty skill name would send allowedSkills:[""], which the real
		// server accepts — creating a carrier agent WITHOUT the skill and letting
		// the eval false-pass against an agent that never had the tool. Fail
		// closed on the dataset instead.
		return carrierAgentSpec{}, fmt.Errorf("skill point snapshot.skill.name required")
	}
	agentCfg, ok := p.Snapshot["agent"].(map[string]any)
	if !ok {
		return carrierAgentSpec{}, fmt.Errorf("skill point snapshot.agent required")
	}
	model, _ := agentCfg["model"].(string)
	if model == "" {
		// A missing model would marshal llmModel:null and be rejected by the
		// server with a generic binding 400; surface it as a dataset defect.
		return carrierAgentSpec{}, fmt.Errorf("skill point snapshot.agent.model required")
	}
	description, _ := skill["description"].(string)
	return carrierAgentSpec{
		skillName:    skillName,
		description:  description,
		model:        model,
		systemPrompt: agentCfg["system_prompt"],
	}, nil
}

func createCarrierAgent(ctx context.Context, client *httpClient, p point) (string, error) {
	spec, err := parseCarrierAgentSpec(p)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"name":          "eval-carrier-" + p.Key,
		"description":   spec.description,
		"systemPrompt":  spec.systemPrompt,
		"llmModel":      spec.model,
		"maxIterations": DefaultCarrierAgentMaxIterations,
		"allowedSkills": []string{spec.skillName},
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
