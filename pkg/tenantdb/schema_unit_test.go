package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestProvisionTenantSchema_EmptyTenantID_Unit(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for empty tenantID")
	}
}

func TestProvisionTenantSchema_InvalidTenantID_Unit(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "bad tenant!")
	if err == nil {
		t.Fatal("expected error for invalid tenantID")
	}
}
