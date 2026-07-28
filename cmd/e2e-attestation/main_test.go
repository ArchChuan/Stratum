package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDigestPrintsLocalSourceDigest(t *testing.T) {
	t.Parallel()
	root := initCLIRepository(t)
	var stdout bytes.Buffer
	require.NoError(t, run([]string{"digest", "--root", root}, &stdout, &bytes.Buffer{}))
	require.Len(t, strings.TrimSpace(stdout.String()), 64)
}

func TestRunRejectsMissingInputsAndUnknownCommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"generate"}, {"verify"}, {"unknown"}} {
		err := run(args, &bytes.Buffer{}, &bytes.Buffer{})
		require.Error(t, err)
	}
}

func TestRunVerifyRejectsInvalidAcceptanceProfilesBeforeReadingAttestation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		args []string
		want string
	}{
		{args: []string{"verify", "--attestation", "missing.json", "--required-mode", "soak"}, want: "required-profile"},
		{args: []string{"verify", "--attestation", "missing.json", "--required-mode", "soak", "--required-profile", "unknown"}, want: "required-profile"},
		{args: []string{"verify", "--attestation", "missing.json", "--required-mode", "short", "--required-profile", "test"}, want: "short"},
	} {
		err := run(tt.args, &bytes.Buffer{}, &bytes.Buffer{})
		require.ErrorContains(t, err, tt.want)
	}
}

func initCLIRepository(t *testing.T) string {
	t.Helper()
	// Reuse the package's public behavior without coupling CLI tests to its private fixture.
	root := t.TempDir()
	commands := [][]string{
		{"init", "-q"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "init"},
	}
	for _, args := range commands {
		command := exec.Command("git", args...)
		command.Dir = root
		require.NoError(t, command.Run())
	}
	return root
}
