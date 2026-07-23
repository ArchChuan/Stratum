package domain

import "testing"

func TestExperimentCommandValidateRequiresHumanAuditFields(t *testing.T) {
	tests := []struct {
		name    string
		command ExperimentCommand
	}{
		{"actor", ExperimentCommand{ActorType: ActorTypeAdmin, Reason: "reviewed", IdempotencyKey: "key", ExpectedStateVersion: 1}},
		{"actor type", ExperimentCommand{ActorID: "admin-1", Reason: "reviewed", IdempotencyKey: "key", ExpectedStateVersion: 1}},
		{"reason", ExperimentCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, IdempotencyKey: "key", ExpectedStateVersion: 1}},
		{"idempotency key", ExperimentCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "reviewed", ExpectedStateVersion: 1}},
		{"state version", ExperimentCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "reviewed", IdempotencyKey: "key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.command.Validate(); err == nil {
				t.Fatalf("expected missing %s to fail validation", tt.name)
			}
		})
	}
}

func TestExperimentCommandFingerprintIncludesSemanticInput(t *testing.T) {
	base := ExperimentCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "reviewed", IdempotencyKey: "key", ExpectedStateVersion: 1}
	changed := base
	changed.Reason = "different"
	if base.Fingerprint(CommandPromote) == changed.Fingerprint(CommandPromote) {
		t.Fatal("different commands must not share a fingerprint")
	}
}
