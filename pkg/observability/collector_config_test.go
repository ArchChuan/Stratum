package observability

import (
	"os"
	"strings"
	"testing"
)

func TestCollectorConfigsProtectEvaluationExperimentSecurityAndErrors(t *testing.T) {
	paths := []string{
		"../../otel-collector-config.yaml",
		"../../k8s/tracing.yaml",
		"../../k8s/opik-otel-collector.yaml",
	}
	required := []string{
		"otlphttp/opik:",
		"name: evaluation-always",
		"key: stratum.evaluation",
		"name: experiment-always",
		"key: stratum.experiment.id",
		"name: security-always",
		"key: stratum.security_violation",
		"name: error-policy",
		"status_codes: [ERROR]",
		"sampling_percentage: 10",
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
		// agent keep 规则：agent.execute 根 span（SDK 打 stratum.agent.execute=true）
		// 必须被保，绕开 default-policy 10% 盲区。三套 collector 都必须有。
		if !strings.Contains(text, "name: agent-always") {
			t.Errorf("%s missing agent-always keep rule", path)
		}
		if !strings.Contains(text, "key: stratum.agent.execute") {
			t.Errorf("%s missing stratum.agent.execute attribute rule", path)
		}
	}

	// Opik exporter 配置形态：docker-compose 与 k8s/tracing.yaml 走 env 注入，
	// opik-otel-collector.yaml 走写死服务名（集群内依赖已固化）。
	for _, path := range []string{"../../otel-collector-config.yaml", "../../k8s/tracing.yaml"} {
		text := readConfig(t, path)
		for _, fragment := range []string{
			"endpoint: ${env:OPIK_OTLP_ENDPOINT}",
			"projectName: ${env:OPIK_PROJECT}",
			"Comet-Workspace: ${env:OPIK_WORKSPACE}",
			"Authorization: ${env:OPIK_API_KEY}",
		} {
			if !strings.Contains(text, fragment) {
				t.Errorf("%s missing %q", path, fragment)
			}
		}
	}
	opikText := readConfig(t, "../../k8s/opik-otel-collector.yaml")
	for _, fragment := range []string{
		"endpoint: http://opik-backend.opik.svc.cluster.local:8080/v1/private/otel",
		"projectName: stratum",
		"Comet-Workspace: default",
	} {
		if !strings.Contains(opikText, fragment) {
			t.Errorf("opik-otel-collector.yaml missing %q", fragment)
		}
	}
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
