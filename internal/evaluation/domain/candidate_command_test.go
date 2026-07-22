package domain

import "testing"

func TestCandidateCommandFingerprintCoversImmutablePayload(t *testing.T) {
	base := CandidateCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "unsafe",
		IdempotencyKey: "request-1", ExpectedStateVersion: 1}
	first := base.Fingerprint()
	second := base.Fingerprint()
	if first != second {
		t.Fatal("fingerprint is not deterministic")
	}
	variants := []CandidateCommand{base, base, base}
	variants[0].ActorID = "admin-2"
	variants[1].Reason = "changed"
	variants[2].ExpectedStateVersion = 2
	for _, variant := range variants {
		if variant.Fingerprint() == base.Fingerprint() {
			t.Fatalf("payload change not covered: %+v", variant)
		}
	}
}
