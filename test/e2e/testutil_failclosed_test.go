package e2e

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestMemoryE2ERequireModeFailsClosed(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestMemoryE2EFailClosedProbe$")
	cmd.Env = append(os.Environ(), "MEMORY_E2E_FAILURE_PROBE=1", "REQUIRE_MEMORY_E2E=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("require mode accepted an unavailable dependency")
	}
}

func TestMemoryE2EFailClosedProbe(t *testing.T) {
	if os.Getenv("MEMORY_E2E_FAILURE_PROBE") != "1" {
		t.Skip("subprocess probe only")
	}
	handleMemoryDependencyFailure(t, "PostgreSQL", errors.New("unavailable"))
}
