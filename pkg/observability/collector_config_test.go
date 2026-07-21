package observability

import (
	"os"
	"strings"
	"testing"
)

func TestCollectorConfigsProtectEvaluationExperimentSecurityAndErrors(t *testing.T) {
	paths := []string{"../../otel-collector-config.yaml", "../../k8s/tracing.yaml"}
	required := []string{
		"otlphttp/opik:",
		"endpoint: ${env:OPIK_OTLP_ENDPOINT}",
		"projectName: ${env:OPIK_PROJECT}",
		"Comet-Workspace: ${env:OPIK_WORKSPACE}",
		"Authorization: ${env:OPIK_API_KEY}",
		"name: evaluation-always",
		"key: stratum.evaluation",
		"name: experiment-always",
		"key: stratum.experiment.id",
		"name: security-always",
		"key: stratum.security_violation",
		"name: error-policy",
		"status_codes: [ERROR]",
		"sampling_percentage: 10",
		"otlphttp/opik",
	}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(content)
		for _, fragment := range required {
			if !strings.Contains(text, fragment) {
				t.Errorf("%s missing %q", path, fragment)
			}
		}
	}
}
