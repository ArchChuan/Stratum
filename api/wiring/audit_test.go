package wiring

import (
	"testing"
)

func TestBuildAudit_NilDB_ReturnsNil(t *testing.T) {
	a := buildAudit(nil)
	if a != nil {
		t.Fatal("buildAudit(nil) should return nil")
	}
}
