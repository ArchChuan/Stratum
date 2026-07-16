package process

import (
	"strings"
	"testing"
)

func TestClassifierRequiresAllFragments(t *testing.T) {
	c := mustClassifier(t, []Rule{{Name: "chroma", AllArgsContain: []string{"chroma-mcp", "stdio"}}})
	if got := c.Classify(Process{Args: []string{"node", "chroma-mcp"}}); got != "" {
		t.Fatalf("Classify = %q; want no match", got)
	}
	if got := c.Classify(Process{Args: []string{"node", "chroma-mcp", "--transport=stdio"}}); got != "chroma" {
		t.Fatalf("Classify = %q; want chroma", got)
	}
}

func TestClassifierFragmentCannotSpanArgumentBoundaries(t *testing.T) {
	c := mustClassifier(t, []Rule{{Name: "service", AllArgsContain: []string{"mcp-server"}}})
	if got := c.Classify(Process{Args: []string{"mcp-", "server"}}); got != "" {
		t.Fatalf("Classify = %q; fragment matched across argv boundary", got)
	}
}

func TestClassifierIgnoresCommandName(t *testing.T) {
	c := mustClassifier(t, []Rule{{Name: "service", AllArgsContain: []string{"mcp-server"}}})
	if got := c.Classify(Process{Command: "mcp-server"}); got != "" {
		t.Fatalf("Classify = %q; command alone must not match", got)
	}
}

func TestClassifierUsesFirstConfiguredMatch(t *testing.T) {
	c := mustClassifier(t, []Rule{
		{Name: "first", AllArgsContain: []string{"server"}},
		{Name: "second", AllArgsContain: []string{"mcp"}},
	})
	if got := c.Classify(Process{Args: []string{"mcp-server"}}); got != "first" {
		t.Fatalf("Classify = %q; want first", got)
	}
}

func TestClassifierTreatsRegexMetacharactersLiterally(t *testing.T) {
	c := mustClassifier(t, []Rule{{Name: "literal", AllArgsContain: []string{".*"}}})
	if got := c.Classify(Process{Args: []string{"anything"}}); got != "" {
		t.Fatalf("Classify = %q; regex metacharacters must not match as a pattern", got)
	}
	if got := c.Classify(Process{Args: []string{"prefix.*suffix"}}); got != "literal" {
		t.Fatalf("Classify = %q; want literal", got)
	}
}

func TestNewClassifierAcceptsDistinctMatchersContainingNUL(t *testing.T) {
	_, err := NewClassifier([]Rule{
		{Name: "first", AllArgsContain: []string{"a", "b\x00c"}},
		{Name: "second", AllArgsContain: []string{"a\x00b", "c"}},
	})
	if err != nil {
		t.Fatalf("NewClassifier rejected structurally distinct matchers: %v", err)
	}
}

func TestNewClassifierRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name  string
		rules []Rule
		want  string
	}{
		{"empty rules", nil, "rule"},
		{"empty name", []Rule{{AllArgsContain: []string{"x"}}}, "name"},
		{"duplicate name", []Rule{{Name: "x", AllArgsContain: []string{"a"}}, {Name: "x", AllArgsContain: []string{"b"}}}, "duplicate"},
		{"empty fragments", []Rule{{Name: "x"}}, "fragment"},
		{"empty fragment", []Rule{{Name: "x", AllArgsContain: []string{""}}}, "fragment"},
		{"duplicate fragment", []Rule{{Name: "x", AllArgsContain: []string{"a", "a"}}}, "duplicate"},
		{"identical matcher", []Rule{{Name: "x", AllArgsContain: []string{"a", "b"}}, {Name: "y", AllArgsContain: []string{"b", "a"}}}, "identical"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClassifier(tt.rules)
			if err == nil {
				t.Fatal("NewClassifier succeeded; want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func mustClassifier(t *testing.T, rules []Rule) *Classifier {
	t.Helper()
	c, err := NewClassifier(rules)
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}
	return c
}
