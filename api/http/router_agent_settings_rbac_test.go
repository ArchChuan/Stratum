package http

import (
	"os"
	"strings"
	"testing"
)

func TestSystemAssistantSettingsRoutesUseMemberReadAndAdminActiveWrite(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	getRoute := `agents.GET("/system/settings", agentHandler.GetSettings)`
	putRoute := `agents.PUT("/system/settings", requireAdmin, requireActive, agentHandler.UpdateModel)`
	idRoute := `agents.GET("/:id", agentHandler.GetAgent)`
	if !strings.Contains(source, getRoute) || !strings.Contains(source, putRoute) {
		t.Fatal("system assistant settings role boundary is missing")
	}
	if strings.Index(source, getRoute) > strings.Index(source, idRoute) ||
		strings.Index(source, putRoute) > strings.Index(source, idRoute) {
		t.Fatal("static system settings routes must be registered before /:id")
	}
}

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
