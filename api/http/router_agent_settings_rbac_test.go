package http

import (
	"os"
	"strings"
	"testing"
)

func TestGeneralAgentMutationRoutesRemainAdminActiveProtected(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, route := range []string{
		`agents.PUT("/:id", requireAdmin, requireActive, agentHandler.UpdateAgent)`,
		`agents.DELETE("/:id", requireAdmin, requireActive, agentHandler.DeleteAgent)`,
	} {
		if !strings.Contains(source, route) {
			t.Fatalf("managed mutation conflict must stay behind admin and active tenant middleware: %s", route)
		}
	}
}
