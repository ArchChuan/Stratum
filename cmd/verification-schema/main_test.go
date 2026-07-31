package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunValidatesJSONDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.json")
	valid := filepath.Join(dir, "valid.json")
	invalid := filepath.Join(dir, "invalid.json")
	require.NoError(t, os.WriteFile(schema, []byte(`{"type":"object","required":["ok"]}`), 0o600))
	require.NoError(t, os.WriteFile(valid, []byte(`{"ok":true}`), 0o600))
	require.NoError(t, os.WriteFile(invalid, []byte(`{}`), 0o600))

	require.NoError(t, run([]string{"--schema", schema, "--input", valid}))
	require.Error(t, run([]string{"--schema", schema, "--input", invalid}))
}

func TestRunRequiresPaths(t *testing.T) {
	t.Parallel()
	require.Error(t, run(nil))
}
