package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// llmExecutor evaluates agent cases with deterministic assertions or real-LLM
// judging. The skill executor (Task 5) reuses this type and overrides
// resolveAgentID to create a temporary carrier agent.
type llmExecutor struct {
	agents  *agentClient
	judge   *judgeClient
	agentID string // resolved from point snapshot
}

func init() {
	registerExecutor("agent", func() executor { return &llmExecutor{} })
}

func (e *llmExecutor) Execute(ctx context.Context, o options, p point) (execResult, error) {
	token, err := ownerTokenFor(o)
	if err != nil {
		// A broken auth setup (missing tenant/user/JWT key) is an environment
		// failure, not a defect: surface it as infra (exit 2).
		return execResult{}, &infraError{err}
	}
	e.agents = &agentClient{client: newHTTPClient(o.baseURL, token)}
	if p.Judge != nil {
		e.judge = newJudgeClient(p.Judge, apiKeyFromEnv(p.Judge.APIKeyEnv))
	}
	agentID, err := e.resolveAgentID(ctx, p)
	if err != nil {
		return execResult{}, err
	}
	e.agentID = agentID
	dataset, err := loadLLMSet(p)
	if err != nil {
		return execResult{}, err
	}
	out, err := e.runCases(ctx, dataset)
	if err != nil {
		return execResult{}, err
	}
	out.Evidence = append(out.Evidence, evidence{Kind: "agent", Ref: agentID})
	return out, nil
}

// resolveAgentID uses the point's declared agent id for agent kind; the skill
// executor overrides this to create a temporary carrier agent (Task 5).
func (e *llmExecutor) resolveAgentID(ctx context.Context, p point) (string, error) {
	id, ok := p.Snapshot["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("agent point snapshot.id required")
	}
	return id, nil
}

// runCases executes every case and rolls up pass_rate/judge_mean/latency.
func (e *llmExecutor) runCases(ctx context.Context, dataset goldenSet) (execResult, error) {
	res := execResult{Cases: []caseOutcome{}}
	var pass, judgeSum float64
	judgeCount := 0
	var latSum int64
	for _, tc := range dataset.Cases {
		outcome := caseOutcome{CaseID: tc.ID, AssertionMode: tc.Mode}
		start := time.Now()
		out, err := e.agents.executeAgent(ctx, e.agentID, tc.Query)
		outcome.LatencyMS = time.Since(start).Milliseconds()
		latSum += outcome.LatencyMS
		if err != nil {
			outcome.Error = err.Error()
			res.Cases = append(res.Cases, outcome)
			continue
		}
		if score, judged := e.applyCase(ctx, tc, out, &outcome); judged {
			judgeSum += score
			judgeCount++
		}
		if outcome.Passed {
			pass++
		}
		res.Cases = append(res.Cases, outcome)
	}
	agg := aggregate{CaseCount: len(res.Cases), AvgLatencyMS: avgInt64(latSum, len(res.Cases))}
	if len(res.Cases) > 0 {
		agg.PassRate = pass / float64(len(res.Cases))
	}
	if judgeCount > 0 {
		agg.JudgeMean = judgeSum / float64(judgeCount)
	}
	res.Aggregate = agg
	return res, nil
}

// applyCase fills the outcome for one produced output under the case's mode.
// It returns the judge score and whether the case was actually judged, so the
// caller can aggregate judge_mean only over successful judge verdicts.
func (e *llmExecutor) applyCase(ctx context.Context, tc goldenCase, out string, outcome *caseOutcome) (float64, bool) {
	switch tc.Mode {
	case AssertExact, AssertContains, AssertRegex:
		if expectedOf(tc) == "" {
			outcome.Error = "assertion case requires expected value"
		} else if err := assertOutput(tc.Mode, out, expectedOf(tc)); err != nil {
			outcome.Error = err.Error()
		} else {
			outcome.Passed = true
		}
	case "judge":
		if e.judge == nil {
			outcome.Error = "judge case requires point.judge config"
			return 0, false
		}
		verdict, err := e.judge.Judge(ctx, tc.JudgeSpec, out)
		if err != nil {
			outcome.Error = err.Error()
			return 0, false
		}
		outcome.Passed = verdict.Passed
		outcome.JudgeScore = verdict.Score
		outcome.JudgeReason = verdict.Reason
		return verdict.Score, true
	default:
		outcome.Error = fmt.Sprintf("unsupported assertion mode %q for llm kind", tc.Mode)
	}
	return 0, false
}

func avgInt64(sum int64, n int) int64 {
	if n == 0 {
		return 0
	}
	return sum / int64(n)
}

// apiKeyFromEnv resolves the judge API key from the named environment
// variable. Empty env name yields an empty key (unauthenticated judge).
func apiKeyFromEnv(envName string) string {
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}
