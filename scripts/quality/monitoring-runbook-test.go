//go:build ignore

package main

import (
	"bytes"
	"fmt"
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
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		var document yaml.Node
		if unmarshalErr := yaml.Unmarshal(data, &document); unmarshalErr != nil {
			return fmt.Errorf("parse %s: %w", path, unmarshalErr)
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
	pathAndAnchor := strings.Split(runbookURL, "#")
	if len(pathAndAnchor) != 2 || !strings.HasPrefix(pathAndAnchor[0], "/docs/") || pathAndAnchor[1] == "" {
		return fmt.Errorf("alert %s has invalid runbook_url %q", alertName, runbookURL)
	}
	repositoryFile := filepath.Join(repositoryRoot, strings.TrimPrefix(pathAndAnchor[0], "/"))
	data, err := os.ReadFile(repositoryFile)
	if err != nil {
		return fmt.Errorf("read runbook for %s: %w", alertName, err)
	}
	expected := []byte("<a id=\"" + pathAndAnchor[1] + "\"></a>\n\n## " + alertName + "\n")
	if !bytes.Contains(data, expected) {
		return fmt.Errorf("runbook heading not found for %s: %s", alertName, runbookURL)
	}
	return nil
}
