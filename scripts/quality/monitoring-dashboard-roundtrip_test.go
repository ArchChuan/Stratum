package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDashboardRoundTripPreservesApostropheAndNewline(t *testing.T) {
	dir := t.TempDir()
	manifest := writeDashboardFixture(t, dir, `{"title":"Gin 的 FullPath()","note":"operator's\nline"}`)

	require.NoError(t, validateDashboardRoundTrip(manifest, dir))
}

func TestValidateDashboardRoundTripRejectsSourceMismatch(t *testing.T) {
	dir := t.TempDir()
	manifest := writeDashboardFixture(t, dir, `{"title":"source"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dashboardFilenames[0]), []byte(`{"title":"changed"}`), 0o600))

	require.ErrorContains(t, validateDashboardRoundTrip(manifest, dir), "differs from source")
}

func writeDashboardFixture(t *testing.T, dir, payload string) string {
	t.Helper()
	var items strings.Builder
	for index, name := range dashboardNames {
		filename := dashboardFilenames[index]
		require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(payload), 0o600))
		fmt.Fprintf(&items, "  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: %s\n    data:\n      %s: |-\n        %s\n", name, filename, payload)
	}
	manifest := filepath.Join(dir, "dashboards.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte("apiVersion: v1\nkind: List\nitems:\n"+items.String()), 0o600))
	return manifest
}
