//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: monitoring-runbook-test <rules-dir> <repository-root>")
		os.Exit(2)
	}
	if err := validateRules(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateRules(rulesDir, repositoryRoot string) error {
	files, err := filepath.Glob(filepath.Join(rulesDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("list monitoring rules: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no monitoring rule files found in %s", rulesDir)
	}
	alertCount := 0
	for _, path := range files {
		file, readErr := os.Open(path)
		if readErr != nil {
			return fmt.Errorf("open %s: %w", path, readErr)
		}
		var document yaml.Node
		decoder := yaml.NewDecoder(file)
		if decodeErr := decoder.Decode(&document); decodeErr != nil {
			file.Close()
			return fmt.Errorf("parse %s: %w", path, decodeErr)
		}
		var trailing yaml.Node
		trailingErr := decoder.Decode(&trailing)
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", path, closeErr)
		}
		if trailingErr != io.EOF {
			if trailingErr != nil {
				return fmt.Errorf("parse trailing document in %s: %w", path, trailingErr)
			}
			return fmt.Errorf("multiple YAML documents are not allowed in %s", path)
		}
		if duplicateErr := rejectDuplicateMappingKeys(&document); duplicateErr != nil {
			return fmt.Errorf("validate mapping keys in %s: %w", path, duplicateErr)
		}
		count, validateErr := validateDocument(&document, repositoryRoot)
		if validateErr != nil {
			return fmt.Errorf("validate %s: %w", path, validateErr)
		}
		alertCount += count
	}
	if alertCount == 0 {
		return fmt.Errorf("no custom alerts found in %s", rulesDir)
	}
	return validateRunbookContracts(repositoryRoot)
}

func rejectDuplicateMappingKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index].Value
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate mapping key %q", key)
			}
			seen[key] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := rejectDuplicateMappingKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func validateDocument(document *yaml.Node, repositoryRoot string) (int, error) {
	root := document
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	groups := mappingValues(root, "groups")
	if len(groups) != 1 || groups[0].Kind != yaml.SequenceNode {
		return 0, fmt.Errorf("expected exactly one groups sequence")
	}
	count := 0
	for _, group := range groups[0].Content {
		rules := mappingValues(group, "rules")
		if len(rules) != 1 || rules[0].Kind != yaml.SequenceNode {
			return 0, fmt.Errorf("expected exactly one rules sequence per group")
		}
		for _, rule := range rules[0].Content {
			alerts := mappingValues(rule, "alert")
			if len(alerts) == 0 {
				continue
			}
			if len(alerts) != 1 || alerts[0].Kind != yaml.ScalarNode || alerts[0].Value == "" {
				return 0, fmt.Errorf("each alert rule must have exactly one non-empty alert name")
			}
			alertName := alerts[0].Value
			annotations := mappingValues(rule, "annotations")
			if len(annotations) != 1 || annotations[0].Kind != yaml.MappingNode {
				return 0, fmt.Errorf("alert %s must have exactly one annotations mapping", alertName)
			}
			runbookURLs := mappingValues(annotations[0], "runbook_url")
			if len(runbookURLs) != 1 || runbookURLs[0].Kind != yaml.ScalarNode {
				return 0, fmt.Errorf("alert %s must have exactly one runbook_url", alertName)
			}
			if err := validateRunbook(alertName, runbookURLs[0].Value, repositoryRoot); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

func mappingValues(mapping *yaml.Node, key string) []*yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	var values []*yaml.Node
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			values = append(values, mapping.Content[index+1])
		}
	}
	return values
}

func validateRunbook(alertName, runbookURL, repositoryRoot string) error {
	rawPath, anchor, found := strings.Cut(runbookURL, "#")
	if !found || anchor == "" || strings.Contains(anchor, "#") {
		return fmt.Errorf("alert %s has invalid runbook_url %q", alertName, runbookURL)
	}
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return fmt.Errorf("decode runbook path for %s: %w", alertName, err)
	}
	if !strings.HasPrefix(decodedPath, "/docs/") || strings.Contains(decodedPath, `\`) ||
		strings.Contains(decodedPath, "//") {
		return fmt.Errorf("alert %s has unsafe runbook path %q", alertName, rawPath)
	}
	for _, segment := range strings.Split(strings.TrimPrefix(decodedPath, "/"), "/") {
		if segment == "." || segment == ".." || segment == "" {
			return fmt.Errorf("alert %s has unsafe runbook path %q", alertName, rawPath)
		}
	}
	docsRoot, err := filepath.Abs(filepath.Join(repositoryRoot, "docs"))
	if err != nil {
		return fmt.Errorf("resolve docs root: %w", err)
	}
	repositoryFile, err := filepath.Abs(filepath.Join(repositoryRoot, strings.TrimPrefix(decodedPath, "/")))
	if err != nil {
		return fmt.Errorf("resolve runbook path for %s: %w", alertName, err)
	}
	if err := requireContained(docsRoot, repositoryFile); err != nil {
		return fmt.Errorf("validate runbook path for %s: %w", alertName, err)
	}
	realDocsRoot, err := filepath.EvalSymlinks(docsRoot)
	if err != nil {
		return fmt.Errorf("resolve real docs root: %w", err)
	}
	realRepositoryFile, err := filepath.EvalSymlinks(repositoryFile)
	if err != nil {
		return fmt.Errorf("resolve real runbook for %s: %w", alertName, err)
	}
	if err := requireContained(realDocsRoot, realRepositoryFile); err != nil {
		return fmt.Errorf("validate real runbook path for %s: %w", alertName, err)
	}
	data, err := os.ReadFile(realRepositoryFile)
	if err != nil {
		return fmt.Errorf("read runbook for %s: %w", alertName, err)
	}
	expected := []byte("<a id=\"" + anchor + "\"></a>\n\n## " + alertName + "\n")
	if !bytes.Contains(data, expected) {
		return fmt.Errorf("runbook heading not found for %s: %s", alertName, runbookURL)
	}
	return nil
}

func requireContained(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s is outside %s", candidate, root)
	}
	return nil
}

func validateRunbookContracts(repositoryRoot string) error {
	contracts := map[string][]string{
		"operations/alerts/monitoring-system.md": {
			`feishu_alert_delivery_total{status="failed"}`,
			`紧急度：warning。查询`,
			`increase(prometheus_rule_evaluation_failures_total[10m])`,
		},
		"operations/alerts/dependencies.md": {
			`up{namespace="stratum",service="stratum-etcd-metrics",endpoint="metrics"}`,
			`up{namespace="stratum",service="stratum-milvus-metrics",endpoint="metrics"}`,
		},
		"operations/alerts/workloads.md": {
			`increase(kube_pod_container_status_restarts_total{namespace="stratum"}[10m])`,
		},
		"operations/remote-monitoring-runbook.md": {
			`3. 用临时 `,
			`4. 保留消息时间`,
		},
	}
	for relativePath, required := range contracts {
		path := filepath.Join(repositoryRoot, "docs", relativePath)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read runbook contract %s: %w", relativePath, err)
		}
		for _, text := range required {
			if !bytes.Contains(data, []byte(text)) {
				return fmt.Errorf("runbook %s is missing required contract %q", relativePath, text)
			}
		}
	}
	return nil
}
