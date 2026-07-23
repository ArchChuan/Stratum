//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type guestResponse struct {
	AccessToken string `json:"access_token"`
	TenantID    string `json:"tenant_id"`
	User        struct {
		Sub string `json:"sub"`
	} `json:"user"`
}

func main() {
	if len(os.Args) != 3 {
		panic("usage: bootstrap.go work-dir run-id")
	}
	workDir, runID := os.Args[1], os.Args[2]
	apiURL, postgresContainer := mustEnv("E2E_API_URL"), mustEnv("E2E_POSTGRES_CONTAINER")
	adminClient := client()
	adminGuest := guest(adminClient, apiURL)
	memberClient := client()
	memberGuest := guest(memberClient, apiURL)
	if adminGuest.TenantID != memberGuest.TenantID {
		panic("isolated guest identities did not share the generated default tenant")
	}
	execSQL(postgresContainer, "elevate generated admin", fmt.Sprintf(`UPDATE public.tenant_members SET role='admin' WHERE tenant_id=%s AND user_id=%s`,
		literal(adminGuest.TenantID), literal(adminGuest.User.Sub)))
	adminToken := refresh(adminClient, apiURL)
	liveMCP := executeLiveMCPFlow(adminClient, apiURL, adminToken, postgresContainer, adminGuest.TenantID, runID)
	liveAgent := executeLiveAgentFlow(adminClient, apiURL, adminToken, liveMCP["serverId"].(string), runID)
	liveSkill := executeLiveSkillFlow(adminClient, apiURL, adminToken, liveAgent["resourceId"].(string),
		liveMCP["serverId"].(string), runID)
	liveKnowledge := executeLiveKnowledgeFlow(adminClient, apiURL, adminToken, runID)

	prefix := strings.ReplaceAll(runID, "-", "_")
	schema := quoteIdentifier(tenantSchemaName(adminGuest.TenantID))
	tenantDDL, err := os.ReadFile(filepath.Join(mustEnv("E2E_REPO_DIR"), "pkg/storage/postgres/tenant_schema.sql"))
	if err != nil {
		panic("read tenant schema baseline failed")
	}
	execSQL(postgresContainer, "apply tenant schema baseline", fmt.Sprintf(
		"BEGIN; SET LOCAL search_path TO %s, public; %s COMMIT;", schema, tenantDDL))
	kinds := []string{"skill", "agent", "mcp", "knowledge"}
	suite, suiteRevision := prefix+"_suite", prefix+"_suite_revision"
	var sql strings.Builder
	fmt.Fprintf(&sql, `INSERT INTO %s.eval_suites(id,name,description,active_revision_id) VALUES(%s,%s,'E2E lifecycle evidence',%s);`,
		schema, literal(suite), literal(runID+"-suite"), literal(suiteRevision))
	fmt.Fprintf(&sql, `INSERT INTO %s.eval_suite_revisions(id,suite_id,version_no,status,resource_kind,published_at) VALUES(%s,%s,1,'published','skill',now());`,
		schema, literal(suiteRevision), literal(suite))

	resources := map[string]any{}
	for index, kind := range kinds {
		id := runID + "-" + kind
		baseline, candidate := id+"-baseline", id+"-candidate"
		experiment := id + "-experiment"
		recommendation, decision, status := "promote", "promote", "running"
		if kind == "mcp" || kind == "knowledge" {
			recommendation, decision, status = "rollback", "rollback", "rolled_back"
		}
		fmt.Fprintf(&sql, `INSERT INTO %s.resource_revisions(id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary,published_at) VALUES(%s,%s,%s,'manual','published',%s,%s,%s,%s::jsonb,now()),(%s,%s,%s,'optimization','published',%s,%s,%s,%s::jsonb,now());`,
			schema, literal(baseline), literal(kind), literal(id), literal("content-"+baseline), literal("payload-"+baseline), literal("minio://encrypted/"+baseline), literal(fmt.Sprintf(`{"label":"baseline","bounded":true,"index":%d}`, index)),
			literal(candidate), literal(kind), literal(id), literal("content-"+candidate), literal("payload-"+candidate), literal("minio://encrypted/"+candidate), literal(`{"label":"candidate","bounded":true}`))
		for _, revision := range []string{baseline, candidate} {
			fmt.Fprintf(&sql, `INSERT INTO %s.eval_runs(id,resource_kind,resource_id,revision_id,suite_revision_id,status,passed,total_cases,passed_cases,idempotency_key,completed_at) VALUES(%s,%s,%s,%s,%s,'succeeded',true,1,1,%s,now());`, schema,
				literal(revision+"-run"), literal(kind), literal(id), literal(revision), literal(suiteRevision), literal(revision+"-run-key"))
		}
		job := id + "-optimization"
		fmt.Fprintf(&sql, `INSERT INTO %s.optimization_jobs(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status,idempotency_key) VALUES(%s,%s,%s,%s,%s,'succeeded',%s);`, schema,
			literal(job), literal(kind), literal(id), literal(baseline), literal(suiteRevision), literal(job+"-key"))
		fmt.Fprintf(&sql, `INSERT INTO %s.optimization_candidates(id,optimization_job_id,revision_id,parent_revision_id,source,status,state_version,eval_run_id) VALUES(%s,%s,%s,%s,'bounded_e2e','proposed',1,%s);`, schema,
			literal(candidate+"-record"), literal(job), literal(candidate), literal(baseline), literal(candidate+"-run"))
		fmt.Fprintf(&sql, `INSERT INTO %s.evaluation_experiments(id,resource_kind,resource_id,stable_revision_id,canary_revision_id,suite_revision_id,status,stage_percent,recommendation,state_version,policy,decision_snapshot,completed_at) VALUES(%s,%s,%s,%s,%s,%s,%s,5,%s,1,'{}','{"metrics":{"samples":100,"observed_minutes":60,"quality_improvement":0.2,"quality_significant":true}}',CASE WHEN %s='running' THEN NULL ELSE now() END);`, schema,
			literal(experiment), literal(kind), literal(id), literal(baseline), literal(candidate), literal(suiteRevision), literal(status), literal(recommendation), literal(status))
		fmt.Fprintf(&sql, `INSERT INTO %s.evaluation_deployments(resource_kind,resource_id,stable_revision_id,canary_revision_id,canary_percent,experiment_id) VALUES(%s,%s,%s,%s,5,%s);`, schema,
			literal(kind), literal(id), literal(baseline), literal(candidate), literal(experiment))
		fmt.Fprintf(&sql, `INSERT INTO %s.experiment_decisions(id,experiment_id,action,actor_type,actor_id,prior_status,new_status,recommendation,reason,idempotency_key) VALUES(%s,%s,%s,'human',%s,'running',%s,%s,'explicit E2E audit decision',%s);`, schema,
			literal(experiment+"-decision"), literal(experiment), literal(decision), literal(adminGuest.User.Sub), literal(status), literal(recommendation), literal(experiment+"-decision-key"))
		resources[kind] = map[string]any{"id": id, "baselineRevision": baseline, "candidateRevision": candidate,
			"experimentId": experiment, "recommendation": recommendation, "decision": decision}
	}
	execSQL(postgresContainer, "seed isolated evaluation lifecycle", sql.String())

	manifest := map[string]any{"tenantId": adminGuest.TenantID, "userId": adminGuest.User.Sub,
		"foreignResourceId": runID + "-foreign", "resources": resources,
		"liveEvidence": map[string]any{"mcp": liveMCP, "agent": liveAgent, "skill": liveSkill,
			"knowledge": liveKnowledge},
		"failureScenarios": []map[string]any{
			{"name": "llm_failure", "resourceKind": "agent", "resourceId": runID + "-agent", "stableRevision": runID + "-agent-baseline"},
			{"name": "security_stop", "resourceKind": "mcp", "resourceId": runID + "-mcp", "stableRevision": runID + "-mcp-baseline"},
			{"name": "dependency_outage", "resourceKind": "knowledge", "resourceId": runID + "-knowledge", "stableRevision": runID + "-knowledge-baseline"},
		}, "ids": map[string]string{"memberDenied": runID + "-member-denied", "duplicate": runID + "-duplicate"}}
	writeJSON(filepath.Join(workDir, "manifest.json"), manifest)
	writeEnv(filepath.Join(workDir, "fixture.env"), map[string]string{"E2E_FIXTURE_MANIFEST": filepath.Join(workDir, "manifest.json"),
		"E2E_ADMIN_TOKEN": adminToken, "E2E_MEMBER_TOKEN": memberGuest.AccessToken})
}

func executeLiveKnowledgeFlow(client *http.Client, apiURL, token, runID string) map[string]any {
	workspaceName := runID + "-live-knowledge"
	var workspace struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/knowledge/workspaces", token, map[string]any{
		"name": workspaceName, "description": "isolated live Knowledge retrieval",
		"config": map[string]any{"embedding_model": "text-embedding-v3", "chunking_strategy": "recursive",
			"chunk_size": 128, "chunk_overlap": 16, "query_mode": "vector", "top_k": 1},
	}, http.StatusCreated, &workspace)
	if workspace.ID == "" {
		panic("live Knowledge workspace creation response invalid")
	}

	documentText := "The evolution center recovery code is bounded-knowledge-42."
	var ingest struct {
		DocumentID string `json:"document_id"`
		Status     string `json:"status"`
	}
	requestMultipart(client, apiURL+"/knowledge/ingest", token, workspaceName,
		"evolution-evidence.txt", []byte(documentText), &ingest)
	if ingest.DocumentID == "" || ingest.Status != "processing" {
		panic("live Knowledge ingest acceptance invalid")
	}
	completed := false
	for range 90 {
		var documents struct {
			Documents []struct {
				ID              string `json:"id"`
				Status          string `json:"ingest_status"`
				ProcessedChunks int    `json:"processed_chunks"`
			} `json:"documents"`
		}
		requestJSON(client, http.MethodGet, apiURL+"/knowledge/workspaces/"+url.PathEscape(workspaceName)+"/documents",
			token, nil, http.StatusOK, &documents)
		for _, document := range documents.Documents {
			if document.ID != ingest.DocumentID {
				continue
			}
			if document.Status == "failed" {
				panic("live Knowledge ingest failed")
			}
			completed = document.Status == "completed" && document.ProcessedChunks > 0
		}
		if completed {
			break
		}
		time.Sleep(time.Second)
	}
	if !completed {
		panic("live Knowledge ingest polling timed out")
	}

	query := "What is the evolution center recovery code?"
	var directQuery struct {
		Sources []struct {
			DocumentID string  `json:"document_id"`
			Content    string  `json:"content"`
			ChunkIndex int     `json:"chunk_index"`
			Score      float64 `json:"score"`
		} `json:"sources"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/knowledge/query", token, map[string]any{
		"question": query, "workspace": workspaceName, "mode": "vector", "topK": 1,
	}, http.StatusOK, &directQuery)
	if len(directQuery.Sources) != 1 || directQuery.Sources[0].DocumentID != ingest.DocumentID ||
		!strings.Contains(directQuery.Sources[0].Content, "bounded-knowledge-42") ||
		directQuery.Sources[0].ChunkIndex != 0 {
		panic("live Knowledge direct retrieval identity invalid")
	}

	var baseline struct {
		RevisionID string `json:"revision_id"`
	}
	requestJSON(client, http.MethodPost,
		apiURL+"/evaluations/resources/knowledge/"+url.PathEscape(workspaceName)+"/baseline", token,
		nil, http.StatusCreated, &baseline)
	if baseline.RevisionID == "" {
		panic("live Knowledge baseline response invalid")
	}
	var suite struct {
		Suite struct {
			ID string `json:"id"`
		} `json:"suite"`
	}
	expected := map[string]any{"relevant": true, "citation_correct": true, "no_answer": false,
		"retrieved_count": 1, "retrieved_document_ids": []string{ingest.DocumentID}}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites", token, map[string]any{
		"name": runID + " Knowledge retrieval", "description": "isolated live Milvus retrieval",
		"resource_kind": "knowledge", "cases": []map[string]any{{"name": "retrieves the ingested document",
			"input": map[string]any{"query": query, "relevant_document_ids": []string{ingest.DocumentID},
				"citation_document_ids": []string{ingest.DocumentID}},
			"expected_output": expected, "assertion_mode": "exact"}},
	}, http.StatusCreated, &suite)
	var suiteRevision struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites/"+suite.Suite.ID+"/publish", token,
		nil, http.StatusOK, &suiteRevision)

	successJob, successRun := executeStoredEvaluationRun(client, apiURL, token, "knowledge", workspaceName,
		baseline.RevisionID, suiteRevision.ID, runID+"-live-knowledge-success")
	assertKnowledgeRun(successJob, successRun, baseline.RevisionID, ingest.DocumentID, true)

	milvusContainer := mustEnv("E2E_MILVUS_CONTAINER")
	assertMilvusPublishedPort(mustEnv("E2E_MILVUS_PUBLISHED_PORT"))
	assertMilvusContainerHealthy(milvusContainer)
	setMilvusProxyEnabled(false)
	failureJob, failureRun := executeStoredEvaluationRun(client, apiURL, token, "knowledge", workspaceName,
		baseline.RevisionID, suiteRevision.ID, runID+"-live-knowledge-outage")
	assertKnowledgeRun(failureJob, failureRun, baseline.RevisionID, ingest.DocumentID, false)
	assertMilvusContainerHealthy(milvusContainer)
	setMilvusProxyEnabled(true)
	assertMilvusPublishedPort(mustEnv("E2E_MILVUS_PUBLISHED_PORT"))
	waitForTCP("127.0.0.1:" + mustEnv("E2E_MILVUS_PORT"))
	recoveryJob, recoveryRun := executeStoredEvaluationRun(client, apiURL, token, "knowledge", workspaceName,
		baseline.RevisionID, suiteRevision.ID, runID+"-live-knowledge-recovery")
	assertKnowledgeRun(recoveryJob, recoveryRun, baseline.RevisionID, ingest.DocumentID, true)

	return map[string]any{"resourceId": workspaceName, "workspaceId": workspace.ID,
		"revisionId": baseline.RevisionID, "documentId": ingest.DocumentID,
		"chunkIndex": directQuery.Sources[0].ChunkIndex, "jobId": successJob.JobID, "runId": successRun.ID,
		"failureJobId": failureJob.JobID, "failureRunId": failureRun.ID,
		"failureError":  failureRun.Results[0].Error,
		"recoveryJobId": recoveryJob.JobID, "recoveryRunId": recoveryRun.ID}
}

func assertKnowledgeRun(
	job evaluationJobResponse,
	run agentEvaluationRun,
	revisionID, documentID string,
	wantPassed bool,
) {
	if job.Status != "succeeded" || job.ResultID != run.ID || run.Resource.RevisionID != revisionID ||
		run.Passed != wantPassed || len(run.Results) != 1 || run.Results[0].Passed != wantPassed {
		panic("live Knowledge evaluation state invalid")
	}
	if !wantPassed {
		if run.Results[0].Error == "" || strings.Contains(strings.ToLower(run.Results[0].Error), "milvus") {
			panic("live Knowledge dependency failure was not sanitized")
		}
		return
	}
	actual, ok := run.Results[0].Actual.(map[string]any)
	if !ok || actual["relevant"] != true || actual["citation_correct"] != true ||
		actual["retrieved_count"] != float64(1) {
		panic("live Knowledge relevance evidence invalid")
	}
	ids, ok := actual["retrieved_document_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != documentID {
		panic("live Knowledge citation identity invalid")
	}
}

func executeStoredEvaluationRun(
	client *http.Client,
	apiURL, token, kind, resourceID, revisionID, suiteRevisionID, idempotencyKey string,
) (evaluationJobResponse, agentEvaluationRun) {
	var queued struct {
		JobID string `json:"job_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/runs", token, map[string]any{
		"resource":          map[string]any{"kind": kind, "resource_id": resourceID, "revision_id": revisionID},
		"suite_revision_id": suiteRevisionID, "idempotency_key": idempotencyKey,
	}, http.StatusAccepted, &queued)
	job := waitForEvaluationJob(client, apiURL, token, queued.JobID)
	if job.Status != "succeeded" || job.ResultID == "" {
		panic("stored evaluation job did not persist its outcome")
	}
	var run agentEvaluationRun
	requestJSON(client, http.MethodGet, apiURL+"/evaluations/runs/"+job.ResultID, token, nil, http.StatusOK, &run)
	return job, run
}

func requestMultipart(
	client *http.Client,
	endpoint, token, workspace, filename string,
	content []byte,
	output any,
) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("workspace", workspace)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		panic("create Knowledge multipart file failed")
	}
	if _, err := part.Write(content); err != nil {
		panic("write Knowledge multipart file failed")
	}
	if err := writer.Close(); err != nil {
		panic("close Knowledge multipart request failed")
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		panic("create Knowledge ingest request failed")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		panic("Knowledge ingest request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		panic(fmt.Sprintf("Knowledge ingest request failed: status=%d", response.StatusCode))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		panic("decode Knowledge ingest response failed")
	}
}

func waitForTCP(address string) {
	for range 90 {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(time.Second)
	}
	panic("dependency TCP readiness timed out")
}

func waitForContainerHealthy(containerID string) {
	for range 90 {
		output, err := exec.Command("docker", "inspect", "-f",
			"{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", containerID).Output()
		if err == nil && strings.TrimSpace(string(output)) == "running healthy" {
			return
		}
		time.Sleep(time.Second)
	}
	writeContainerFailureDiagnostics(containerID)
	panic("dependency container health polling timed out")
}

var (
	bearerDiagnosticPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;}]+`)
	secretDiagnosticPattern = regexp.MustCompile(
		`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret|credential)` +
			`(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}]+)`,
	)
)

func sanitizeContainerDiagnostic(value string) string {
	value = bearerDiagnosticPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = secretDiagnosticPattern.ReplaceAllString(value, "$1$2[REDACTED]")
	if len(value) > 500 {
		return value[:500] + " [TRUNCATED]"
	}
	return value
}

func writeContainerFailureDiagnostics(containerID string) {
	type healthLog struct {
		Start    string `json:"Start"`
		End      string `json:"End"`
		ExitCode int    `json:"ExitCode"`
		Output   string `json:"Output"`
	}
	type containerState struct {
		Status string `json:"Status"`
		Paused bool   `json:"Paused"`
		Health *struct {
			Status string      `json:"Status"`
			Log    []healthLog `json:"Log"`
		} `json:"Health"`
	}

	output, err := exec.Command("docker", "inspect", "-f", "{{json .State}}", containerID).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "milvus-diagnostic: id=%s inspect=failed\n", containerID)
		return
	}
	var state containerState
	if err := json.Unmarshal(output, &state); err != nil {
		fmt.Fprintf(os.Stderr, "milvus-diagnostic: id=%s state=invalid\n", containerID)
		return
	}
	healthStatus := "none"
	if state.Health != nil {
		healthStatus = state.Health.Status
	}
	fmt.Fprintf(os.Stderr, "milvus-diagnostic: id=%s status=%s paused=%t health=%s\n",
		containerID, state.Status, state.Paused, healthStatus)
	if state.Health != nil {
		start := max(0, len(state.Health.Log)-5)
		for _, entry := range state.Health.Log[start:] {
			fmt.Fprintf(os.Stderr, "milvus-health: start=%s end=%s exit=%d output=%s\n",
				entry.Start, entry.End, entry.ExitCode, sanitizeContainerDiagnostic(strings.TrimSpace(entry.Output)))
		}
	}

	logs, err := exec.Command("docker", "logs", "--tail", "200", containerID).CombinedOutput()
	if err != nil && len(logs) == 0 {
		fmt.Fprintln(os.Stderr, "milvus-log: unavailable")
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(logs))
	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "health") && !strings.Contains(lower, "error") &&
			!strings.Contains(lower, "unhealthy") && !strings.Contains(lower, "fatal") &&
			!strings.Contains(lower, "panic") {
			continue
		}
		fmt.Fprintf(os.Stderr, "milvus-log: %s\n", sanitizeContainerDiagnostic(line))
	}
}

func assertMilvusContainerHealthy(containerID string) {
	output, err := exec.Command("docker", "inspect", "-f",
		"{{.Id}} {{.State.Status}} {{.State.Paused}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}",
		containerID).Output()
	want := containerID + " running false healthy"
	if err != nil || strings.TrimSpace(string(output)) != want {
		writeContainerFailureDiagnostics(containerID)
		panic("isolated Milvus container identity or health changed")
	}
}

func setMilvusProxyEnabled(enabled bool) {
	payload, _ := json.Marshal(map[string]bool{"enabled": enabled})
	response, err := http.Post(mustEnv("E2E_MILVUS_PROXY_URL")+"/mode", "application/json", bytes.NewReader(payload))
	if err != nil {
		panic("change Milvus proxy mode failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		panic("change Milvus proxy mode returned unexpected status")
	}
	var state struct {
		Enabled           bool `json:"enabled"`
		ActiveConnections int  `json:"active_connections"`
	}
	response, err = http.Get(mustEnv("E2E_MILVUS_PROXY_URL") + "/state")
	if err != nil {
		panic("read Milvus proxy state failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(response.Body, 1<<10)).Decode(&state) != nil || state.Enabled != enabled ||
		(!enabled && state.ActiveConnections != 0) {
		panic("Milvus proxy state invalid")
	}
}

func assertMilvusPublishedPort(want string) {
	output, err := exec.Command("docker", "compose", "-p", mustEnv("E2E_COMPOSE_PROJECT"),
		"-f", mustEnv("E2E_COMPOSE_FILE"), "port", "milvus", "19530").Output()
	if err != nil {
		panic("query isolated Milvus published port failed")
	}
	address := strings.TrimSpace(string(output))
	_, got, err := net.SplitHostPort(address)
	if err != nil || got != want {
		panic("isolated Milvus published port changed")
	}
}

func executeLiveSkillFlow(client *http.Client, apiURL, token, agentID, serverID, runID string) map[string]any {
	var workspace struct {
		Skill struct {
			ID string `json:"id"`
		} `json:"skill"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/skills", token, map[string]any{
		"name": runID + " live skill", "goal": "Return the bounded evaluation result",
		"whenToUse":   "When executing the isolated Skill evaluation scenario",
		"sampleInput": "Run the live Skill scenario.", "expectedOutput": "bounded-agent-result",
		"instructions": "Return bounded-agent-result after following the active scenario.",
		"requirements": map[string]any{"mcpToolIds": []string{}, "knowledgeWorkspaceIds": []string{},
			"memoryScopes": []string{}},
	}, http.StatusCreated, &workspace)
	if workspace.Skill.ID == "" {
		panic("live Skill creation response invalid")
	}
	requestJSON(client, http.MethodPatch, apiURL+"/skills/"+workspace.Skill.ID+"/draft/activation", token,
		map[string]any{"name": "e2e_live_skill", "description": "Isolated live Skill activation",
			"inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"},
			"confirmed": true}, http.StatusOK, nil)
	var published struct {
		ID      string `json:"id"`
		SkillID string `json:"skillId"`
		Status  string `json:"status"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/skills/"+workspace.Skill.ID+"/publish", token,
		nil, http.StatusOK, &published)
	if published.ID == "" || published.SkillID != workspace.Skill.ID || published.Status != "published" {
		panic("live Skill publish response invalid")
	}

	requestJSON(client, http.MethodPut, apiURL+"/agents/"+agentID, token, map[string]any{
		"name": runID + " live agent", "type": "react", "description": "isolated live Agent execution",
		"systemPrompt": "Call the provided lookup tool exactly once, then return the bounded result.",
		"llmModel":     "qwen-plus", "maxIterations": 4, "maxContextTokens": 4096,
		"allowedSkills":         []string{workspace.Skill.ID},
		"mcpToolIds":            []string{"mcp:" + serverID + ":e2e_lookup"},
		"knowledgeWorkspaceIds": []string{},
	}, http.StatusOK, nil)

	var suiteResult struct {
		Suite struct {
			ID string `json:"id"`
		} `json:"suite"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites", token, map[string]any{
		"name": runID + " Skill scenario", "description": "isolated live Skill execution",
		"resource_kind": "skill", "cases": []map[string]any{{
			"name": "executes exact published Skill revision", "input": "Run the live Skill scenario.",
			"expected_output": "bounded-agent-result", "assertion_mode": "exact",
		}},
	}, http.StatusCreated, &suiteResult)
	var suiteRevision struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites/"+suiteResult.Suite.ID+"/publish", token,
		nil, http.StatusOK, &suiteRevision)
	requestsBefore := readCounter(mustEnv("E2E_LLM_EVIDENCE"), "requests")
	var queued struct {
		JobID string `json:"job_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/runs", token, map[string]any{
		"resource":          map[string]any{"kind": "skill", "resource_id": workspace.Skill.ID, "revision_id": published.ID},
		"suite_revision_id": suiteRevision.ID, "idempotency_key": runID + "-live-skill-run",
	}, http.StatusAccepted, &queued)
	job := waitForEvaluationJob(client, apiURL, token, queued.JobID)
	if job.Status != "succeeded" || job.ResultID == "" {
		panic("live Skill evaluation job did not succeed")
	}
	var run agentEvaluationRun
	requestJSON(client, http.MethodGet, apiURL+"/evaluations/runs/"+job.ResultID, token, nil, http.StatusOK, &run)
	requestDelta := readCounter(mustEnv("E2E_LLM_EVIDENCE"), "requests") - requestsBefore
	if !run.Passed || len(run.Results) != 1 || !run.Results[0].Passed ||
		run.Results[0].Actual != "bounded-agent-result" || run.Results[0].TraceID == "" || run.Results[0].Tokens <= 0 ||
		run.Resource.RevisionID != published.ID || requestDelta != 1 {
		panic("live Skill evaluation evidence invalid")
	}
	return map[string]any{"resourceId": workspace.Skill.ID, "revisionId": published.ID,
		"suiteRevisionId": suiteRevision.ID, "jobId": queued.JobID, "runId": run.ID,
		"traceId": run.Results[0].TraceID, "tokens": run.Results[0].Tokens, "llmRequests": requestDelta}
}

func executeLiveAgentFlow(client *http.Client, apiURL, token, serverID, runID string) map[string]any {
	const providerMarker = "e2e-provider-marker"
	requestJSON(client, http.MethodPatch, apiURL+"/tenant/settings", token, map[string]any{
		"settings": map[string]any{"llm_api_keys": map[string]string{"qwen": providerMarker}},
	}, http.StatusOK, nil)
	var settings map[string]any
	requestJSON(client, http.MethodGet, apiURL+"/tenant/settings", token, nil, http.StatusOK, &settings)
	encodedSettings, _ := json.Marshal(settings)
	if bytes.Contains(encodedSettings, []byte(providerMarker)) {
		panic("tenant settings returned plaintext provider credential")
	}

	var agent struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/agents", token, map[string]any{
		"name": runID + " live agent", "type": "react",
		"description":  "isolated live Agent execution",
		"systemPrompt": "Call the provided lookup tool exactly once, then return the bounded result.",
		"llmModel":     "qwen-plus", "maxIterations": 4, "maxContextTokens": 4096,
		"mcpToolIds": []string{"mcp:" + serverID + ":e2e_lookup"},
	}, http.StatusCreated, &agent)
	if agent.ID == "" {
		panic("live Agent creation response invalid")
	}
	var baseline struct {
		Kind       string `json:"kind"`
		ResourceID string `json:"resource_id"`
		RevisionID string `json:"revision_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/resources/agent/"+agent.ID+"/baseline", token,
		nil, http.StatusCreated, &baseline)
	if baseline.Kind != "agent" || baseline.ResourceID != agent.ID || baseline.RevisionID == "" {
		panic("live Agent baseline response invalid")
	}
	var suiteResult struct {
		Suite struct {
			ID string `json:"id"`
		} `json:"suite"`
		Revision struct {
			ID string `json:"id"`
		} `json:"revision"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites", token, map[string]any{
		"name": runID + " Agent tool execution", "description": "isolated live Agent execution",
		"resource_kind": "agent", "cases": []map[string]any{{
			"name": "calls real MCP tool", "input": "Use the lookup tool and return bounded-agent-result.",
			"expected_output": "bounded-agent-result", "assertion_mode": "exact",
		}},
	}, http.StatusCreated, &suiteResult)
	var published struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites/"+suiteResult.Suite.ID+"/publish", token,
		nil, http.StatusOK, &published)
	var queued struct {
		JobID string `json:"job_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/runs", token, map[string]any{
		"resource":          map[string]any{"kind": "agent", "resource_id": agent.ID, "revision_id": baseline.RevisionID},
		"suite_revision_id": published.ID, "idempotency_key": runID + "-live-agent-run",
	}, http.StatusAccepted, &queued)
	job := waitForEvaluationJob(client, apiURL, token, queued.JobID)
	if job.Status != "succeeded" || job.ResultID == "" {
		panic("live Agent evaluation job did not succeed")
	}
	var run struct {
		ID      string `json:"id"`
		Passed  bool   `json:"passed"`
		Results []struct {
			Passed  bool   `json:"passed"`
			Actual  any    `json:"actual"`
			TraceID string `json:"trace_id"`
			Tokens  int    `json:"tokens"`
		} `json:"results"`
	}
	requestJSON(client, http.MethodGet, apiURL+"/evaluations/runs/"+job.ResultID, token, nil, http.StatusOK, &run)
	if !run.Passed || len(run.Results) != 1 || !run.Results[0].Passed ||
		run.Results[0].Actual != "bounded-agent-result" || run.Results[0].TraceID == "" || run.Results[0].Tokens <= 0 {
		panic("live Agent evaluation run evidence invalid")
	}
	var toolEvidence struct {
		ToolTraces []map[string]any `json:"tool_traces"`
	}
	waitForJSON(client, apiURL+"/agents/executions/"+run.Results[0].TraceID+"/tool-traces", token, &toolEvidence)
	var traceEvidence struct {
		TraceEvents []map[string]any `json:"trace_events"`
	}
	waitForJSON(client, apiURL+"/agents/executions/"+run.Results[0].TraceID+"/trace-events", token, &traceEvidence)
	if len(toolEvidence.ToolTraces) == 0 || len(traceEvidence.TraceEvents) == 0 {
		panic("live Agent trace evidence missing")
	}
	toolJSON, _ := json.Marshal(toolEvidence)
	traceJSON, _ := json.Marshal(traceEvidence)
	if !bytes.Contains(toolJSON, []byte(run.Results[0].TraceID)) ||
		!bytes.Contains(toolJSON, []byte("e2e_lookup")) ||
		!bytes.Contains(traceJSON, []byte(run.Results[0].TraceID)) {
		panic("live Agent trace evidence identity mismatch")
	}
	if readCounter(mustEnv("E2E_MCP_EVIDENCE"), "calls") != 2 ||
		readCounter(mustEnv("E2E_LLM_EVIDENCE"), "requests") != 2 {
		panic("live Agent provider call evidence invalid")
	}

	requestJSON(client, http.MethodPost, mustEnv("E2E_LLM_URL")+"/mode", token,
		map[string]bool{"failure": true}, http.StatusNoContent, nil)
	failureJob, failureRun := executeAgentEvaluationRun(client, apiURL, token, agent.ID, baseline.RevisionID,
		published.ID, runID+"-live-agent-provider-failure")
	if failureJob.Status != "succeeded" || failureRun.Passed || len(failureRun.Results) != 1 ||
		failureRun.Results[0].Passed || failureRun.Results[0].Error == "" {
		panic("live Agent provider failure was not persisted as a failed case outcome")
	}
	failureJSON, _ := json.Marshal(failureRun)
	for _, forbidden := range []string{providerMarker, "provider unavailable", "raw upstream", "upstream body", "upstream response"} {
		if bytes.Contains(bytes.ToLower(failureJSON), []byte(strings.ToLower(forbidden))) {
			panic("live Agent provider failure exposed sensitive upstream evidence")
		}
	}

	requestJSON(client, http.MethodPost, mustEnv("E2E_LLM_URL")+"/mode", token,
		map[string]bool{"failure": false}, http.StatusNoContent, nil)
	recoveryJob, recoveryRun := executeAgentEvaluationRun(client, apiURL, token, agent.ID, baseline.RevisionID,
		published.ID, runID+"-live-agent-provider-recovery")
	if recoveryJob.Status != "succeeded" || !recoveryRun.Passed || len(recoveryRun.Results) != 1 ||
		!recoveryRun.Results[0].Passed || recoveryRun.Results[0].Actual != "bounded-agent-result" ||
		recoveryRun.Resource.RevisionID != baseline.RevisionID {
		panic("live Agent stable revision did not recover after provider restoration")
	}
	return map[string]any{"resourceId": agent.ID, "revisionId": baseline.RevisionID,
		"suiteRevisionId": published.ID, "jobId": queued.JobID, "runId": run.ID,
		"traceId": run.Results[0].TraceID, "tokens": run.Results[0].Tokens,
		"toolTraces": len(toolEvidence.ToolTraces), "traceEvents": len(traceEvidence.TraceEvents),
		"failureJobId": failureJob.JobID, "failureRunId": failureRun.ID,
		"failureError":  failureRun.Results[0].Error,
		"recoveryJobId": recoveryJob.JobID, "recoveryRunId": recoveryRun.ID}
}

type agentEvaluationRun struct {
	ID       string `json:"id"`
	Resource struct {
		RevisionID string `json:"revision_id"`
	} `json:"resource"`
	Passed  bool `json:"passed"`
	Results []struct {
		Passed  bool   `json:"passed"`
		Actual  any    `json:"actual"`
		Error   string `json:"error"`
		TraceID string `json:"trace_id"`
		Tokens  int    `json:"tokens"`
	} `json:"results"`
}

func executeAgentEvaluationRun(
	client *http.Client,
	apiURL, token, agentID, revisionID, suiteRevisionID, idempotencyKey string,
) (evaluationJobResponse, agentEvaluationRun) {
	var queued struct {
		JobID string `json:"job_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/runs", token, map[string]any{
		"resource":          map[string]any{"kind": "agent", "resource_id": agentID, "revision_id": revisionID},
		"suite_revision_id": suiteRevisionID, "idempotency_key": idempotencyKey,
	}, http.StatusAccepted, &queued)
	job := waitForEvaluationJob(client, apiURL, token, queued.JobID)
	if job.Status != "succeeded" || job.ResultID == "" {
		panic("live Agent evaluation job did not persist its outcome")
	}
	var run agentEvaluationRun
	requestJSON(client, http.MethodGet, apiURL+"/evaluations/runs/"+job.ResultID, token, nil, http.StatusOK, &run)
	return job, run
}

func waitForJSON(client *http.Client, url, token string, output any) {
	const pollingBudget = 90 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), pollingBudget)
	defer cancel()
	stats := evidencePollingStats{statusCounts: make(map[int]int)}
	for ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			panic("create evidence request failed")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			if timeoutError, ok := err.(interface{ Timeout() bool }); ok && timeoutError.Timeout() {
				stats.transportTimeouts++
				stats.lastStatus = "transport-timeout"
			}
			if ctx.Err() != nil {
				break
			}
			panic("evidence request failed")
		}
		stats.statusCounts[response.StatusCode]++
		stats.lastStatus = fmt.Sprintf("status=%d", response.StatusCode)
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
			_ = response.Body.Close()
			if err != nil {
				panic("decode evidence response failed")
			}
			if hasNonEmptyEvidence(output) {
				return
			}
			stats.emptyOK++
			stats.lastStatus = "status=200-empty"
			if !waitForEvidenceRetry(ctx) {
				break
			}
			continue
		}
		_ = response.Body.Close()
		if !isTransientEvidenceStatus(response.StatusCode) {
			panic(fmt.Sprintf("evidence request failed: status=%d", response.StatusCode))
		}
		if !waitForEvidenceRetry(ctx) {
			break
		}
	}
	writeEvidencePollingTimeoutDiagnostics(stats)
	panic("evidence polling timed out")
}

type evidencePollingStats struct {
	statusCounts      map[int]int
	transportTimeouts int
	emptyOK           int
	lastStatus        string
}

func (stats evidencePollingStats) summary() string {
	statuses := make([]int, 0, len(stats.statusCounts))
	for status := range stats.statusCounts {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	counts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		counts = append(counts, fmt.Sprintf("%d:%d", status, stats.statusCounts[status]))
	}
	return fmt.Sprintf("statuses=%s transport-timeouts=%d empty-200=%d last=%s",
		strings.Join(counts, ","), stats.transportTimeouts, stats.emptyOK, stats.lastStatus)
}

func writeEvidencePollingTimeoutDiagnostics(stats evidencePollingStats) {
	fmt.Fprintf(os.Stderr, "evidence-polling-timeout: %s\n", stats.summary())
	writeCollectorSpanMetrics()
	writeOpikBackendState()
}

func writeCollectorSpanMetrics() {
	port := os.Getenv("E2E_OTEL_METRICS_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "collector-span-metrics: unavailable")
		return
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://127.0.0.1:" + port + "/metrics")
	if err != nil {
		fmt.Fprintln(os.Stderr, "collector-span-metrics: unreachable")
		return
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "otelcol_exporter_sent_spans") &&
			!strings.HasPrefix(line, "otelcol_exporter_send_failed_spans") {
			continue
		}
		found = true
		fmt.Fprintf(os.Stderr, "collector-span-metric: %s\n", line)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "collector-span-metrics: status=%d no-span-series\n", response.StatusCode)
	}
}

func writeOpikBackendState() {
	project, composeFile, overrideFile := os.Getenv("E2E_OPIK_PROJECT"),
		os.Getenv("E2E_OPIK_COMPOSE_FILE"), os.Getenv("E2E_OPIK_OVERRIDE_FILE")
	if project == "" || composeFile == "" || overrideFile == "" {
		fmt.Fprintln(os.Stderr, "opik-backend-state: unavailable")
		return
	}
	containerID, err := exec.Command("docker", "compose", "-p", project, "-f", composeFile,
		"-f", overrideFile, "ps", "-q", "backend").Output()
	if err != nil || strings.TrimSpace(string(containerID)) == "" {
		fmt.Fprintln(os.Stderr, "opik-backend-state: container-unavailable")
		return
	}
	state, err := exec.Command("docker", "inspect", "-f",
		"id={{.Id}} status={{.State.Status}} running={{.State.Running}} restart-count={{.RestartCount}} "+
			"health={{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}} exit={{.State.ExitCode}} "+
			"oom={{.State.OOMKilled}}", strings.TrimSpace(string(containerID))).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "opik-backend-state: inspect-failed")
		return
	}
	fmt.Fprintf(os.Stderr, "opik-backend-state: %s\n", strings.TrimSpace(string(state)))
}

func isTransientEvidenceStatus(status int) bool {
	return status == http.StatusNotFound || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func hasNonEmptyEvidence(output any) bool {
	value := reflect.ValueOf(output)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return false
	}
	value = value.Elem()
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return !value.IsZero()
	}
	for i := range value.NumField() {
		field := value.Field(i)
		if field.CanInterface() && !field.IsZero() {
			return true
		}
	}
	return false
}

func waitForEvidenceRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func readCounter(path, label string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		panic("read sanitized helper evidence failed")
	}
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), label+"=%d", &value); err != nil {
		panic("parse sanitized helper evidence failed")
	}
	return value
}

func executeLiveMCPFlow(client *http.Client, apiURL, token, postgresContainer, tenantID, runID string) map[string]any {
	serverID := runID + "-live-mcp"
	requestJSON(client, http.MethodPost, apiURL+"/mcp/servers", token, map[string]any{
		"id": serverID, "name": "Isolated evaluation MCP", "version": "1.0.0", "transport": "http",
		"url": mustEnv("E2E_MCP_URL"), "timeout": int64(5 * time.Second), "capabilities": []string{"tools"},
		"env": map[string]string{}, "headers": map[string]string{},
	}, http.StatusCreated, nil)
	requestJSON(client, http.MethodPut, apiURL+"/mcp/tool-policies/"+url.PathEscape(serverID)+"/e2e_lookup", token,
		map[string]string{"riskLevel": "read"}, http.StatusOK, nil)
	var baseline struct {
		Kind       string `json:"kind"`
		ResourceID string `json:"resource_id"`
		RevisionID string `json:"revision_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/resources/mcp/"+serverID+"/baseline", token,
		nil, http.StatusCreated, &baseline)
	if baseline.Kind != "mcp" || baseline.ResourceID != serverID || baseline.RevisionID == "" {
		panic("live MCP baseline response invalid")
	}
	var suiteResult struct {
		Suite struct {
			ID string `json:"id"`
		} `json:"suite"`
		Revision struct {
			ID string `json:"id"`
		} `json:"revision"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites", token, map[string]any{
		"name": runID + " MCP contract", "description": "isolated live MCP execution", "resource_kind": "mcp",
		"cases": []map[string]any{{"name": "calls real MCP tool", "input": map[string]any{
			"tool": "e2e_lookup", "arguments": map[string]any{"id": runID}},
			"expected_output": map[string]any{"status": "success"}, "assertion_mode": "exact"}},
	}, http.StatusCreated, &suiteResult)
	if suiteResult.Suite.ID == "" || suiteResult.Revision.ID == "" {
		panic("live MCP suite response invalid")
	}
	var published struct {
		ID string `json:"id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/suites/"+suiteResult.Suite.ID+"/publish", token,
		nil, http.StatusOK, &published)
	if published.ID != suiteResult.Revision.ID {
		panic("live MCP suite publication mismatch")
	}
	var queued struct {
		JobID string `json:"job_id"`
	}
	requestJSON(client, http.MethodPost, apiURL+"/evaluations/runs", token, map[string]any{
		"resource":          map[string]any{"kind": "mcp", "resource_id": serverID, "revision_id": baseline.RevisionID},
		"suite_revision_id": published.ID, "idempotency_key": runID + "-live-mcp-run",
	}, http.StatusAccepted, &queued)
	job := waitForEvaluationJob(client, apiURL, token, queued.JobID)
	if job.Status != "succeeded" || job.ResultID == "" {
		panic("live MCP evaluation job did not succeed")
	}
	var run struct {
		ID      string `json:"id"`
		Passed  bool   `json:"passed"`
		Results []struct {
			Passed bool           `json:"passed"`
			Actual map[string]any `json:"actual"`
		} `json:"results"`
	}
	requestJSON(client, http.MethodGet, apiURL+"/evaluations/runs/"+job.ResultID, token, nil, http.StatusOK, &run)
	if !run.Passed || len(run.Results) != 1 || !run.Results[0].Passed || run.Results[0].Actual["status"] != "success" {
		panic("live MCP evaluation run evidence invalid")
	}
	evidence, err := os.ReadFile(mustEnv("E2E_MCP_EVIDENCE"))
	if err != nil || strings.TrimSpace(string(evidence)) != "calls=1" {
		panic("live MCP network call evidence invalid")
	}
	verifyEncryptedRevisionObject(postgresContainer, tenantID, baseline.RevisionID, serverID, mustEnv("E2E_MCP_URL"))
	return map[string]any{"serverId": serverID, "revisionId": baseline.RevisionID,
		"suiteRevisionId": published.ID, "jobId": queued.JobID, "runId": run.ID,
		"toolCalls": 1, "encryptedPayloadVerified": true}
}

func verifyEncryptedRevisionObject(postgresContainer, tenantID, revisionID string, plaintextMarkers ...string) {
	schema := quoteIdentifier(tenantSchemaName(tenantID))
	row := querySQL(postgresContainer, fmt.Sprintf(
		`SELECT payload_ref || '|' || payload_hash FROM %s.resource_revisions WHERE id=%s`,
		schema, literal(revisionID)))
	parts := strings.Split(row, "|")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		panic("encrypted revision reference evidence invalid")
	}
	ref, err := url.Parse(parts[0])
	if err != nil || ref.Scheme != "object" || ref.Host != mustEnv("TRACE_PAYLOAD_BUCKET") || ref.Path == "" {
		panic("encrypted revision object reference invalid")
	}
	client, err := minio.New(mustEnv("E2E_MINIO_ENDPOINT"), &minio.Options{
		Creds:  credentials.NewStaticV4(mustEnv("TRACE_PAYLOAD_ACCESS_KEY"), mustEnv("TRACE_PAYLOAD_SECRET_KEY"), ""),
		Secure: false,
	})
	if err != nil {
		panic("create MinIO evidence client failed")
	}
	object, err := client.GetObject(context.Background(), ref.Host, strings.TrimPrefix(ref.Path, "/"), minio.GetObjectOptions{})
	if err != nil {
		panic("read encrypted revision object failed")
	}
	defer object.Close()
	ciphertext, err := io.ReadAll(io.LimitReader(object, 1<<20))
	if err != nil || len(ciphertext) == 0 {
		panic("encrypted revision object is unreadable")
	}
	for _, marker := range plaintextMarkers {
		if marker != "" && bytes.Contains(ciphertext, []byte(marker)) {
			panic("encrypted revision object contains plaintext marker")
		}
	}
}

type evaluationJobResponse struct {
	JobID        string `json:"job_id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	ResultID     string `json:"result_id"`
}

func waitForEvaluationJob(client *http.Client, apiURL, token, jobID string) evaluationJobResponse {
	if jobID == "" {
		panic("evaluation job ID required")
	}
	for range 90 {
		var job evaluationJobResponse
		requestJSON(client, http.MethodGet, apiURL+"/evaluations/jobs/"+jobID, token, nil, http.StatusOK, &job)
		if job.Status == "succeeded" || job.Status == "failed" || job.Status == "cancelled" {
			return job
		}
		time.Sleep(time.Second)
	}
	panic("evaluation job polling timed out")
}

func requestJSON(client *http.Client, method, url, token string, payload any, status int, output any) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			panic("encode E2E request failed")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		panic("create E2E request failed")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		panic("E2E API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		panic(fmt.Sprintf("E2E API request failed: method=%s status=%d", method, response.StatusCode))
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
			panic("decode E2E API response failed")
		}
	}
}

func client() *http.Client { jar, _ := cookiejar.New(nil); return &http.Client{Jar: jar} }
func guest(client *http.Client, apiURL string) guestResponse {
	response, err := client.Post(apiURL+"/auth/guest", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 2048)).Decode(&failure)
		panic(fmt.Sprintf("guest login failed: status=%d error=%s", response.StatusCode, safeError(failure.Error)))
	}
	var result guestResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		panic(err)
	}
	return result
}
func refresh(client *http.Client, apiURL string) string {
	response, err := client.Post(apiURL+"/auth/refresh", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
		_ = body
		panic("refresh failed")
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		panic(err)
	}
	return result.AccessToken
}
func execSQL(containerID, operation, sql string) {
	command := exec.Command("docker", "exec", "-i", containerID, "psql", "-U", "stratum_e2e", "-d", "stratum_e2e",
		"-X", "-v", "ON_ERROR_STOP=1", "-q")
	command.Stdin = strings.NewReader(sql)
	output, err := command.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("fixture SQL failed: operation=%s error=%s", operation, safeError(string(output))))
	}
}
func querySQL(containerID, sql string) string {
	command := exec.Command("docker", "exec", "-i", containerID, "psql", "-U", "stratum_e2e", "-d", "stratum_e2e",
		"-XAt", "-v", "ON_ERROR_STOP=1", "-c", sql)
	output, err := command.CombinedOutput()
	if err != nil {
		panic("fixture SQL evidence query failed")
	}
	return strings.TrimSpace(string(output))
}
func mustEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}
func literal(value string) string             { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func tenantSchemaName(tenantID string) string { return "tenant_" + tenantID }
func quoteIdentifier(value string) string     { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func safeError(value string) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{"bearer ", "api_key", "access_token", "credential", "secret", "raw payload", "upstream body", "upstream response"} {
		if strings.Contains(strings.ToLower(value), marker) {
			return "redacted"
		}
	}
	if len(value) > 160 {
		return value[:160]
	}
	if value == "" {
		return "unspecified"
	}
	return value
}
func writeJSON(path string, value any) {
	data, _ := json.Marshal(value)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		panic(err)
	}
}
func writeEnv(path string, values map[string]string) {
	var out strings.Builder
	for key, value := range values {
		fmt.Fprintf(&out, "export %s=%q\n", key, value)
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		panic(err)
	}
}
