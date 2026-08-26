package main

import (
	"fmt"
)

// loadLLMSet validates a skill/agent golden dataset: judge cases require a
// judge_spec, assertion cases require an expected value.
func loadLLMSet(p point) (goldenSet, error) {
	goldenPath, err := resolveRelative(p.Dir, p.Golden)
	if err != nil {
		return goldenSet{}, err
	}
	var set goldenSet
	if err := readYAML(goldenPath, &set); err != nil {
		return set, err
	}
	if set.Version != 1 || len(set.Cases) == 0 {
		return set, fmt.Errorf("llm golden dataset: version must be 1 and cases non-empty")
	}
	for _, tc := range set.Cases {
		if err := validateLLMCase(tc); err != nil {
			return set, err
		}
	}
	return set, nil
}

// validateLLMCase enforces the per-case contract: a query is always required;
// assertion modes need an expected value; judge mode needs judge_spec.criteria.
func validateLLMCase(tc goldenCase) error {
	if tc.Query == "" {
		return fmt.Errorf("case %s: query required", tc.ID)
	}
	switch tc.Mode {
	case AssertExact, AssertContains, AssertRegex:
		if expectedOf(tc) == "" {
			return fmt.Errorf("case %s: assertion mode %q requires expected value", tc.ID, tc.Mode)
		}
	case "judge":
		if tc.JudgeSpec.Criteria == "" {
			return fmt.Errorf("case %s: judge case requires judge_spec.criteria", tc.ID)
		}
	default:
		return fmt.Errorf("case %s: unsupported mode %q (want exact|contains|regex|judge)", tc.ID, tc.Mode)
	}
	return nil
}
