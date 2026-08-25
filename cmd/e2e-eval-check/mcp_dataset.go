package main

import (
	"context"
	"fmt"
)

// loadMCPSet loads the golden cases for an mcp point. The dataset lives at the
// point's golden path; it reuses the generic goldenSet (Query=tool spec,
// Mode=assertion mode, ExpectedOutput=expected value).
func loadMCPSet(ctx context.Context, o options, p point) (goldenSet, error) {
	goldenPath, err := resolveRelative(p.Dir, p.Golden)
	if err != nil {
		return goldenSet{}, err
	}
	var set goldenSet
	if err := readYAML(goldenPath, &set); err != nil {
		return set, err
	}
	if set.Version != 1 || len(set.Cases) == 0 {
		return set, fmt.Errorf("mcp golden dataset: version must be 1 and cases non-empty")
	}
	for _, tc := range set.Cases {
		if expectedOf(tc) == "" {
			return set, fmt.Errorf("mcp case %s: expected value required", tc.ID)
		}
		switch tc.Mode {
		case AssertExact, AssertContains, AssertRegex:
		default:
			return set, fmt.Errorf("mcp case %s: assertion mode %q unsupported (want exact|contains|regex)", tc.ID, tc.Mode)
		}
	}
	return set, nil
}
