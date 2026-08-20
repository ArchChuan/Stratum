package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
)

// e2e-rag-check measures RAG retrieval quality over the real HTTP
// /knowledge/query path against a committed golden dataset, and compares the
// result against a recorded baseline. It is the SKILL's R3 risk-triggered
// spot-check: quantitative, non-blocking (unless --fail-on-warn), with the
// JSON report as the reviewable evidence.
//
// Exit codes: 0 = passed/not_run, 1 = failed (defect or WARN gate), 2 =
// infra_failed (environment, auth, provider). WARNs never change the exit code
// unless --fail-on-warn is set.
func main() {
	code, err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "e2e-rag-check: %v\n", err)
	}
	os.Exit(code)
}

type options struct {
	baseURL        string
	dataset        string
	baselinePath   string
	output         string
	warnDelta      float64
	provider       string
	tenantID       string
	userID         string
	recordBaseline bool
	confirmRecord  bool
	failOnWarn     bool
	skipReason     string
}

func parseFlags(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("e2e-rag-check", flag.ContinueOnError)
	fs.StringVar(&opts.baseURL, "base-url", "http://localhost:8080", "server base URL")
	fs.StringVar(&opts.dataset, "dataset", "", "golden dataset directory (cases.yaml + documents/)")
	fs.StringVar(&opts.baselinePath, "baseline", "", "baseline file to compare against (and to write when recording)")
	fs.StringVar(&opts.output, "output", "", "report file path (JSON); empty skips file write")
	fs.Float64Var(&opts.warnDelta, "warn-delta", DefaultWarnDelta, "aggregate regression WARN threshold (run < baseline - delta)")
	fs.StringVar(&opts.provider, "provider", "", "embedding provider declaration (test tenant identifier); required to record")
	// Test identity falls back to the local .env convention (STRATUM_TEST_*)
	// so a plain `set -a; . ./.env; set +a; go run` works; explicit flags win.
	fs.StringVar(&opts.tenantID, "tenant-id", os.Getenv("STRATUM_TEST_TENANT_ID"), "test tenant id for the owner JWT (env STRATUM_TEST_TENANT_ID)")
	fs.StringVar(&opts.userID, "user-id", os.Getenv("STRATUM_TEST_USER_ID"), "user id for the owner JWT (env STRATUM_TEST_USER_ID)")
	fs.BoolVar(&opts.recordBaseline, "record-baseline", false, "record the baseline after a passing run")
	fs.BoolVar(&opts.confirmRecord, "confirm-record", false, "required alongside --record-baseline (fail-closed gate)")
	fs.BoolVar(&opts.failOnWarn, "fail-on-warn", false, "turn strong WARNs into a failed exit code")
	fs.StringVar(&opts.skipReason, "skip", "", "skip reason; when set, report is not_run and nothing executes")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func statusOf(code int) string {
	switch code {
	case exitInfra:
		return "infra_failed"
	case exitFailed:
		return "failed"
	default:
		return "passed"
	}
}

// run parses flags and dispatches: a --skip reason produces a not_run report
// without executing anything; otherwise execute() runs the real check.
func run(args []string, stdout, stderr io.Writer) (int, error) {
	opts, err := parseFlags(args)
	if err != nil {
		return exitFailed, err
	}

	// --skip is a first-class path: not_run report with a mandatory reason,
	// no execution, no silent bypass.
	if opts.skipReason != "" {
		report := notRunReport(opts)
		if opts.output != "" {
			if err := writeReport(opts.output, report); err != nil {
				return exitFailed, err
			}
		}
		printSummary(stdout, report)
		return exitPassed, nil
	}
	return execute(context.Background(), opts, stdout, stderr)
}

// notRunReport builds the --skip report: not_run with the reason the operator
// gave, so a skip can never masquerade as a green run.
func notRunReport(opts options) Report {
	return newReport("not_run", opts.dataset, "", goldenSnapshot{}, effectiveConfig{},
		providerFingerprint{}, nil, aggregate{}, nil, nil, false, opts.skipReason, nil)
}

// execute is the real run: it loads the dataset, provisions a transient
// workspace, ingests the golden documents, evaluates every case, compares
// against the baseline, and (optionally) records a new baseline. Cleanup and
// report write always happen via the deferred closure, so residual entities
// surface even on early error returns.
func execute(ctx context.Context, opts options, stdout, stderr io.Writer) (code int, _ error) {
	code = exitPassed
	report := Report{Version: 1, GeneratedAt: time.Now().UTC(), DatasetDir: opts.dataset}
	var client *httpClient
	workspace := ""

	// Always attempt cleanup and report write on the way out. LIFO: cleanup
	// (registered after) runs before the report write, so residual entities
	// land in the report.
	defer func() {
		if workspace != "" && client != nil {
			if err := client.deleteWorkspace(ctx, workspace); err != nil {
				report.Residuals = append(report.Residuals, workspace)
			}
		}
		if report.Status == "" {
			report.Status = statusOf(code)
		}
		if opts.output != "" {
			if err := writeReport(opts.output, report); err != nil {
				_, _ = fmt.Fprintf(stderr, "e2e-rag-check: write report: %v\n", err)
			}
		}
		printSummary(stdout, report)
	}()

	golden, documents, err := loadGoldenOrErr(opts)
	if err != nil {
		return exitFailed, err
	}
	client, workspace, created, err := provision(ctx, opts, golden)
	if err != nil {
		return exitCode(err), err
	}
	docIDs, err := ingestGolden(ctx, client, workspace, documents)
	if err != nil {
		return exitCode(err), err
	}
	retriever := &httpEvaluationRetriever{client: client}
	cases, err := evaluateCases(ctx, retriever, golden, docIDs, workspace, created.ID)
	if err != nil {
		return exitCode(err), err
	}
	agg := computeAggregate(cases)
	warnings := buildCaseWarnings(cases)
	config := effectiveConfig{
		EmbeddingModel: created.Config.EmbeddingModel,
		TopK:           created.Config.TopK,
		ScoreThreshold: created.Config.ScoreThreshold,
	}
	fingerprint := fingerprintOf(golden.Snapshot.EmbeddingModel, opts.provider)
	base, driftWarnings, nonComparable, err := resolveBaseline(opts, config, fingerprint, agg)
	if err != nil {
		return exitFailed, err
	}
	warnings = append(warnings, driftWarnings...)
	if opts.recordBaseline {
		base, err = recordBaseline(ctx, opts, config, fingerprint, agg, cases, warnings)
		if err != nil {
			return exitFailed, err
		}
	}
	if opts.failOnWarn {
		code = strongWarnGate(warnings)
	}
	report = newReport(statusOf(code), opts.dataset, workspace, golden.Snapshot, config, fingerprint,
		cases, agg, base, warnings, nonComparable, "", report.Residuals)
	return code, nil
}

// strongWarnGate maps strong WARNs to exitFailed when the --fail-on-warn gate
// is enabled; callers guard with the flag so the gate stays opt-in. Splitting
// the predicate out keeps execute under the complexity budget.
func strongWarnGate(warnings []warning) int {
	if hasStrongWarn(warnings) {
		return exitFailed
	}
	return exitPassed
}

// loadGoldenOrErr returns a dataset defect (exit 1) for a missing dataset or
// invalid golden data — never an infra classification.
func loadGoldenOrErr(opts options) (goldenSet, []goldenDocument, error) {
	if opts.dataset == "" {
		return goldenSet{}, nil, errors.New("--dataset is required (or --skip with a reason)")
	}
	set, documents, err := loadGolden(opts.dataset)
	if err != nil {
		return set, documents, fmt.Errorf("golden dataset: %w", err)
	}
	return set, documents, nil
}

// provision signs the owner JWT, verifies the server is up, creates a
// transient workspace, and probes the embedding provider on the empty
// workspace so provider/environment failures surface here as infra rather than
// mid-evaluation.
func provision(ctx context.Context, opts options, golden goldenSet) (client *httpClient, workspace string, created *workspaceConfig, err error) {
	token, err := mintOwnerToken(opts.tenantID, opts.userID)
	if err != nil {
		return nil, "", nil, &infraError{err}
	}
	client = newHTTPClient(opts.baseURL, token)
	if err := client.health(ctx); err != nil {
		return client, "", nil, &infraError{fmt.Errorf("preflight: %w", err)}
	}
	// Transient workspace: isolation at the workspace level, never the tenant.
	workspace = fmt.Sprintf("%s%d", DefaultWorkspacePrefix, time.Now().UnixNano())
	created, err = client.createWorkspace(ctx, workspace, golden.Snapshot)
	if err != nil {
		return client, workspace, nil, fmt.Errorf("create workspace: %w", err)
	}
	retriever := &httpEvaluationRetriever{client: client}
	if _, err := retriever.Query(ctx, knowledgeapp.RAGQueryRequest{
		Question: "provider availability probe", Workspace: workspace,
		Mode: golden.Snapshot.QueryMode, TopK: golden.Snapshot.TopK,
	}); err != nil {
		return client, workspace, nil, &infraError{fmt.Errorf("embedding provider probe: %w", err)}
	}
	return client, workspace, created, nil
}

// ingestGolden uploads every golden document, waits for ingestion to reach a
// terminal state, then maps source file names to document ids.
func ingestGolden(ctx context.Context, client *httpClient, workspace string, documents []goldenDocument) (map[string]string, error) {
	for _, doc := range documents {
		data, err := os.ReadFile(doc.Path)
		if err != nil {
			return nil, fmt.Errorf("read document %s: %w", doc.Source, err)
		}
		if err := client.ingest(ctx, workspace, doc.Source, data); err != nil {
			return nil, fmt.Errorf("ingest %s: %w", doc.Source, err)
		}
	}
	docs, err := client.waitForIngest(ctx, workspace, documentSources(documents))
	if err != nil {
		return nil, err
	}
	return indexDocuments(docs)
}

// resolveBaseline loads the recorded baseline when present and computes the
// drift/regression warnings against it. A missing baseline is
// baseline.present:false: compareBaseline(nil) suppresses regressions instead
// of treating an empty file as drift against zero values.
func resolveBaseline(opts options, config effectiveConfig, fingerprint providerFingerprint, agg aggregate) (*baseline, []warning, bool, error) {
	var base *baseline
	if opts.baselinePath != "" {
		loaded, err := loadBaseline(opts.baselinePath)
		if err != nil {
			if os.IsNotExist(err) && !opts.recordBaseline {
				return nil, nil, false, fmt.Errorf("baseline %s not found: %w", opts.baselinePath, err)
			}
			if !os.IsNotExist(err) {
				return nil, nil, false, fmt.Errorf("load baseline: %w", err)
			}
			// First recording: the missing file is left as baseline.present:false
			// for the comparison, then overwritten by the record step below.
		} else {
			base = loaded
		}
	}
	driftWarnings, nonComparable := compareBaseline(fingerprint, config, agg, base, opts.warnDelta)
	return base, driftWarnings, nonComparable, nil
}

// recordBaseline persists the run as the new baseline after the fail-closed
// gate. Re-deriving against the freshly recorded baseline would produce no
// drift by construction, so the report's non_comparable flag comes from the
// resolveBaseline comparison, not from here.
func recordBaseline(ctx context.Context, opts options, config effectiveConfig, fingerprint providerFingerprint, agg aggregate, cases []caseResult, warnings []warning) (*baseline, error) {
	if err := recordBaselineGate(cases, warnings, opts.provider, opts.confirmRecord); err != nil {
		return nil, err
	}
	base := &baseline{
		RecordedCommit: shortGitCommit(ctx),
		RecordedAt:     time.Now().UTC().Format(time.RFC3339),
		Provider:       fingerprint,
		Config:         config,
		Aggregate:      agg,
	}
	if err := writeBaseline(opts.baselinePath, base); err != nil {
		return nil, fmt.Errorf("write baseline: %w", err)
	}
	return base, nil
}

// exitCode classifies a step failure: infra (environment/auth/provider) maps
// to exit code 2, everything else is a defect (exit 1).
func exitCode(err error) int {
	if isInfra(err) {
		return exitInfra
	}
	return exitFailed
}

// shortGitCommit is a best-effort anchor for the recorded baseline. "unknown"
// on failure is acceptable: the commit is metadata, not the comparison anchor.
func shortGitCommit(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// loadBaseline reads a recorded baseline. A missing file returns an
// os.IsNotExist error so the caller can distinguish "explicitly absent" from a
// corrupt baseline.
func loadBaseline(path string) (*baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var base baseline
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("decode baseline: %w", err)
	}
	return &base, nil
}

func writeBaseline(path string, base *baseline) error {
	if path == "" {
		return fmt.Errorf("write baseline: --baseline path required when recording")
	}
	data, err := json.Marshal(base)
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create baseline dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	return nil
}
