package application

import (
	"testing"

	parametersdomain "github.com/byteBuilderX/stratum/internal/parameters/domain"
)

// TestCritiqueWhitelistStaysInLockstepWithRegistry pins the optimizer
// critique gate (optimizer_critique.go) against the registry: both directions
// must hold, exactly like the candidate whitelist test.
func TestCritiqueWhitelistStaysInLockstepWithRegistry(t *testing.T) {
	reg := parametersdomain.NewParametersRegistry()

	registered := map[string]bool{}
	for _, key := range reg.EvaluationKeys() {
		registered[key] = true
	}
	for key := range allowedParams {
		if !registered[key] {
			t.Errorf("critique whitelist accepts %q but the registry has no such evaluation key", key)
		}
	}
	for key := range allowedPrompts {
		if !registered[key] {
			t.Errorf("critique prompt whitelist accepts %q but the registry has no such evaluation key", key)
		}
	}
	known := map[string]bool{}
	for key := range allowedParams {
		known[key] = true
	}
	for key := range allowedPrompts {
		known[key] = true
	}
	for _, key := range reg.EvaluationKeys() {
		if !known[key] {
			t.Errorf("registry evaluation key %q is not accepted by the critique whitelist", key)
		}
	}
}
