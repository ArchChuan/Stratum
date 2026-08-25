package domain

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// tenantSchemaSQLPath 相对本包（internal/agent/domain）指向仓库根的 tenant-only DDL 基线。
// DDL 的唯一事实源是 pkg/storage/postgres/tenant_schema.sql，测试从 cwd（包目录）出发读取。
const tenantSchemaSQLPath = "../../../pkg/storage/postgres/tenant_schema.sql"

// addConstraintRe 匹配 DROP+ADD 重建点（启动 provisioning 会对存量租户逐语句执行）。
var addConstraintRe = regexp.MustCompile(
	`ADD CONSTRAINT agent_tool_approvals_status_check\s+CHECK \(status IN \(([^)]*)\)\)`)

// statusInRe 匹配任意一处 status IN (...) 白名单（[^)]* 可跨行）。
var statusInRe = regexp.MustCompile(`status IN \(([^)]*)\)`)

// quoteRe 提取白名单内的 'value' 字面量。
var quoteRe = regexp.MustCompile(`'([^']*)'`)

// TestTenantSchemaApprovalStatusWhitelistCoversAllDomainStatuses 防止 DDL 白名单与
// domain 状态机枚举漂移：曾因 tenant_schema.sql 第一处重建点漏
// cancelled/voided/invalidated，导致对存量 cancelled 审批行 ADD CONSTRAINT 校验失败、
// provisioning 事务回滚、启动 fail-closed、CD 部署 CrashLoopBackOff。
// domain 枚举是唯一事实源，SQL 中每一处 status CHECK 白名单都必须覆盖全部 ToolApprovalStatus。
func TestTenantSchemaApprovalStatusWhitelistCoversAllDomainStatuses(t *testing.T) {
	data, err := os.ReadFile(tenantSchemaSQLPath)
	if err != nil {
		t.Fatalf("read %s: %v", tenantSchemaSQLPath, err)
	}
	sql := string(data)

	var want []string
	for _, s := range []ToolApprovalStatus{
		ToolApprovalPending,
		ToolApprovalApproved,
		ToolApprovalRejected,
		ToolApprovalExpired,
		ToolApprovalExecuting,
		ToolApprovalExecuted,
		ToolApprovalOutcomeUnknown,
		ToolApprovalCancelled,
		ToolApprovalVoided,
		ToolApprovalInvalidated,
	} {
		want = append(want, string(s))
	}

	// 1) 两处 DROP+ADD 重建点（存量租户与全新租户最终约束的来源）。
	adds := addConstraintRe.FindAllStringSubmatch(sql, -1)
	if len(adds) < 2 {
		t.Fatalf("expected >=2 ADD CONSTRAINT agent_tool_approvals_status_check, got %d", len(adds))
	}

	// 2) 建表内联 CHECK（全新租户首建表时先落一个，随后被 ADD 覆盖，仍需与枚举一致）。
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS agent_tool_approvals")
	if createAt < 0 {
		t.Fatalf("CREATE TABLE IF NOT EXISTS agent_tool_approvals not found in %s", tenantSchemaSQLPath)
	}
	inline := statusInRe.FindStringSubmatch(sql[createAt:])
	if inline == nil {
		t.Fatal("inline status CHECK whitelist not found after CREATE TABLE agent_tool_approvals")
	}

	whitelists := [][]string{parseWhitelist(inline[1])}
	for _, m := range adds {
		whitelists = append(whitelists, parseWhitelist(m[1]))
	}
	for i, got := range whitelists {
		for _, s := range want {
			if !containsStr(got, s) {
				t.Fatalf("whitelist #%d %v missing domain status %q", i, got, s)
			}
		}
	}
}

// parseWhitelist 把 status IN 捕获组拆成引号值列表。
func parseWhitelist(body string) []string {
	var out []string
	for _, m := range quoteRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
