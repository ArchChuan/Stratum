package main

import (
	"fmt"
	"regexp"
	"strings"
)

// assertionModes supported by mcp/skill/agent cases.
const (
	AssertExact    = "exact"
	AssertContains = "contains"
	AssertRegex    = "regex"
)

// assertOutput checks a produced output against an expected value under the
// given assertion mode.
func assertOutput(mode, output, expected string) error {
	switch mode {
	case AssertExact:
		if output != expected {
			return fmt.Errorf("exact mismatch: want %q, got %q", expected, output)
		}
	case AssertContains:
		if !strings.Contains(output, expected) {
			return fmt.Errorf("output %q missing expected substring %q", output, expected)
		}
	case AssertRegex:
		re, err := regexp.Compile(expected)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %w", expected, err)
		}
		if !re.MatchString(output) {
			return fmt.Errorf("output %q does not match regex %q", output, expected)
		}
	default:
		return fmt.Errorf("unsupported assertion mode %q", mode)
	}
	return nil
}

// expectedOf returns the expected assertion value for a case: the explicit
// expected_output when set, else the first relevant document (the pre-
// expected_output encoding used by legacy llm/mcp datasets). Returning "" for
// neither lets loaders reject empty expectations, which would otherwise pass
// silently under contains/exact.
func expectedOf(tc goldenCase) string {
	if tc.ExpectedOutput != "" {
		return tc.ExpectedOutput
	}
	if len(tc.RelevantDocuments) > 0 {
		return tc.RelevantDocuments[0]
	}
	return ""
}
