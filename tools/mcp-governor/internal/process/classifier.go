package process

import (
	"fmt"
	"slices"
	"strings"
)

type Rule struct {
	Name           string
	AllArgsContain []string
}

type Classifier struct {
	rules []Rule
}

func NewClassifier(rules []Rule) (*Classifier, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("classifier requires at least one rule")
	}

	names := make(map[string]struct{}, len(rules))
	type canonicalMatcher struct {
		name      string
		fragments []string
	}
	matchers := make([]canonicalMatcher, 0, len(rules))
	validated := make([]Rule, len(rules))
	for i, rule := range rules {
		context := fmt.Sprintf("rule[%d]", i)
		if strings.TrimSpace(rule.Name) == "" {
			return nil, fmt.Errorf("%s name must not be empty", context)
		}
		if _, exists := names[rule.Name]; exists {
			return nil, fmt.Errorf("%s name %q is duplicate", context, rule.Name)
		}
		names[rule.Name] = struct{}{}
		if len(rule.AllArgsContain) == 0 {
			return nil, fmt.Errorf("%s requires at least one fragment", context)
		}

		fragments := make(map[string]struct{}, len(rule.AllArgsContain))
		for j, fragment := range rule.AllArgsContain {
			if fragment == "" {
				return nil, fmt.Errorf("%s fragment[%d] must not be empty", context, j)
			}
			if _, exists := fragments[fragment]; exists {
				return nil, fmt.Errorf("%s fragment %q is duplicate", context, fragment)
			}
			fragments[fragment] = struct{}{}
		}
		canonical := slices.Clone(rule.AllArgsContain)
		slices.Sort(canonical)
		for _, existing := range matchers {
			if slices.Equal(canonical, existing.fragments) {
				return nil, fmt.Errorf("%s has matcher identical to service %q", context, existing.name)
			}
		}
		matchers = append(matchers, canonicalMatcher{name: rule.Name, fragments: canonical})
		validated[i] = Rule{Name: rule.Name, AllArgsContain: slices.Clone(rule.AllArgsContain)}
	}
	return &Classifier{rules: validated}, nil
}

func (c *Classifier) Classify(process Process) string {
	for _, rule := range c.rules {
		if matchesAllArguments(process.Args, rule.AllArgsContain) {
			return rule.Name
		}
	}
	return ""
}

func matchesAllArguments(args, fragments []string) bool {
	for _, fragment := range fragments {
		matched := false
		for _, arg := range args {
			if strings.Contains(arg, fragment) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
