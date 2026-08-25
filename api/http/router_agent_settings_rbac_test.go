package http

import (
	"os"
	"strings"
	"testing"
)

// P1/P2：agent update 门控放宽——白名单成员（editors）可直接编辑，真实鉴权由 service
// ownership 矩阵完成（owner/admin/creator/白名单 editor 放行，其余 ErrForbidden）；
// 故 PUT /:id 与 PUT /:id/editors 不再要求 requireAdmin。delete/create 仍保留 admin 门控
// （delete 路径 service 仍限 creator/owner，此处路由层一并守住）。
func TestGeneralAgentMutationRoutesAdminActiveProtected(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)

	// update/editors：仅 requireActive + requireActive（member 白名单成员可编辑）。
	for _, route := range []string{
		`agents.PUT("/:id", requireActive, agentHandler.UpdateAgent)`,
		`agents.PUT("/:id/editors", requireActive, agentHandler.SetAgentEditors)`,
	} {
		if !strings.Contains(source, route) {
			t.Fatalf("update routes must be member-reachable for whitelisted editors (service matrix enforces): %s", route)
		}
	}

	// delete/create：仍要求 admin。
	for _, route := range []string{
		`agents.DELETE("/:id", requireAdmin, requireActive, agentHandler.DeleteAgent)`,
		`agents.POST("", requireAdmin, requireActive, agentHandler.CreateAgent)`,
	} {
		if !strings.Contains(source, route) {
			t.Fatalf("destructive mutation must stay behind admin and active tenant middleware: %s", route)
		}
	}
}
