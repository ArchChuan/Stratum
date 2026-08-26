package main

import (
	"context"
	"fmt"
	"os"
)

// knowledgeExecutor runs the RAG single-point evaluation over the real HTTP
// server, reusing the migrated knowledge pipeline and returning the unified result.
type knowledgeExecutor struct{}

func init() {
	registerExecutor("knowledge", func() executor { return &knowledgeExecutor{} })
}

// Execute runs the full provision→ingest→evaluate pipeline. The point's
// golden field points at the golden directory (../golden); loadGolden reads
// cases.yaml plus documents/*.md from it. The named result lets the deferred
// cleanup append residuals that survive early error returns.
func (e *knowledgeExecutor) Execute(ctx context.Context, o options, p point) (res execResult, err error) {
	goldenDir, err := resolveRelative(p.Dir, p.Golden)
	if err != nil {
		return res, err
	}
	golden, documents, err := loadGolden(goldenDir)
	if err != nil {
		return res, err
	}
	token, err := ownerTokenFor(o)
	if err != nil {
		// A broken auth setup (missing tenant/user/JWT key) is an environment
		// failure, not a defect: surface it as infra (exit 2).
		return res, &infraError{err}
	}
	client := newHTTPClient(o.baseURL, token)
	if err := client.health(ctx); err != nil {
		// Server-down or auth failures surface here before any provisioning,
		// mirroring the legacy preflight gate (exit 2).
		return res, &infraError{fmt.Errorf("preflight: %w", err)}
	}
	// The workspace name is the resource key across the knowledge HTTP routes
	// (GetByName); created.ID is the DB id and is passed to the evaluator only
	// as workspace metadata.
	workspace := newWorkspaceName(o, p)
	defer func() {
		if workspace != "" {
			if delErr := client.deleteWorkspace(ctx, workspace); delErr != nil {
				res.Residuals = append(res.Residuals, workspace)
			}
		}
	}()
	created, err := client.createWorkspace(ctx, workspace, golden.Snapshot)
	if err != nil {
		return res, err
	}
	res.Evidence = append(res.Evidence, evidence{Kind: "workspace", Ref: workspace})
	docIDs, err := ingestGolden(ctx, client, workspace, documents)
	if err != nil {
		return res, err
	}
	retriever := &httpEvaluationRetriever{client: client}
	results, err := evaluateCases(ctx, retriever, golden, docIDs, workspace, created.ID)
	if err != nil {
		return res, err
	}
	res.Cases = outcomesFromKnowledge(results)
	res.Aggregate = aggregateFromKnowledge(results)
	res.Warnings = buildCaseWarnings(results)
	return res, nil
}

// ingestGolden uploads every golden document, waits for ingestion to reach a
// terminal state, then maps source file names to document ids.
func ingestGolden(ctx context.Context, client *httpClient, workspace string, documents []goldenDocument) (map[string]string, error) {
	for _, doc := range documents {
		data, err := os.ReadFile(doc.Path)
		if err != nil {
			return nil, fmt.Errorf("read golden document %s: %w", doc.Path, err)
		}
		if err := client.ingest(ctx, workspace, doc.Source, data); err != nil {
			return nil, err
		}
	}
	docs, err := client.waitForIngest(ctx, workspace, documentSources(documents))
	if err != nil {
		return nil, err
	}
	return indexDocuments(docs)
}

// newWorkspaceName derives the transient workspace name from the point key. It
// is deterministic so reruns clean up the same resource and evidence/residuals
// stay readable.
func newWorkspaceName(o options, p point) string {
	return DefaultWorkspacePrefix + o.point
}
