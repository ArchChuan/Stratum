package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProvisionTenantSchemaIDsContinuesAndAggregatesFailures(t *testing.T) {
	errA := errors.New("tenant a failed")
	errC := errors.New("tenant c failed")
	var attempted []string

	err := provisionTenantSchemaIDs(context.Background(), []string{"a", "b", "c"}, func(_ context.Context, id string) error {
		attempted = append(attempted, id)
		switch id {
		case "a":
			return errA
		case "c":
			return errC
		default:
			return nil
		}
	})

	if !reflect.DeepEqual(attempted, []string{"a", "b", "c"}) {
		t.Fatalf("attempted = %v", attempted)
	}
	if !errors.Is(err, errA) || !errors.Is(err, errC) {
		t.Fatalf("aggregate error = %v, want both tenant failures", err)
	}
	if got := err.Error(); got == "" || !containsAll(got, "tenant a", "tenant c") {
		t.Fatalf("aggregate error lacks tenant context: %v", err)
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}
