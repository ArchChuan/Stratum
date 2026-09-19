package constants

import "testing"

func TestDynamicRecentGroups(t *testing.T) {
	cases := []struct {
		name   string
		tokens int
		want   int
	}{
		{"zero uses default", 0, LoopCompactionRecentGroups},
		{"negative uses default", -100, LoopCompactionRecentGroups},
		{"below small threshold", 100, CompactionRecentGroupsSmall},
		{"at small threshold is default", CompactionRecentGroupsThresholdSmall, LoopCompactionRecentGroups},
		{"mid range default", 32_000, LoopCompactionRecentGroups},
		{"at large threshold is default", CompactionRecentGroupsThresholdLarge, LoopCompactionRecentGroups},
		{"above large threshold", 100_000, CompactionRecentGroupsLarge},
		{"exactly one above threshold", CompactionRecentGroupsThresholdLarge + 1, CompactionRecentGroupsLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicRecentGroups(tc.tokens); got != tc.want {
				t.Errorf("DynamicRecentGroups(%d) = %d, want %d", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestDynamicSummaryReserve(t *testing.T) {
	cases := []struct {
		name   string
		budget int
		want   int
	}{
		{"zero hits floor", 0, CompactionSummaryReserveFloor},
		{"negative hits floor", -1, CompactionSummaryReserveFloor},
		{"small budget hits floor", 1000, CompactionSummaryReserveFloor},                      // 5% of 1000 = 50 < 200
		{"floor boundary", CompactionSummaryReserveFloor * 20, CompactionSummaryReserveFloor}, // 5% of 4000 = 200
		{"linear above floor", 8000, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicSummaryReserve(tc.budget); got != tc.want {
				t.Errorf("DynamicSummaryReserve(%d) = %d, want %d", tc.budget, got, tc.want)
			}
		})
	}
}

func TestDynamicCompactionMaxTokens(t *testing.T) {
	cases := []struct {
		name   string
		tokens int
		want   int
	}{
		{"tiny hits floor", 100, CompactionMaxTokensFloor},
		{"zero hits floor", 0, CompactionMaxTokensFloor},
		{"negative hits floor", -5, CompactionMaxTokensFloor},
		{"floor boundary", CompactionMaxTokensFloor * 10, CompactionMaxTokensFloor}, // 10% of 4000 = 400
		{"mid linear", 5000, 500},
		{"ceiling boundary", CompactionMaxTokensCeiling * 10, CompactionMaxTokensCeiling}, // 10% of 8000 = 800
		{"above ceiling", 100_000, CompactionMaxTokensCeiling},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicCompactionMaxTokens(tc.tokens); got != tc.want {
				t.Errorf("DynamicCompactionMaxTokens(%d) = %d, want %d", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestCollectionName(t *testing.T) {
	cases := []struct {
		name        string
		workspaceID string
		model       string
		want        string
	}{
		{"uuid workspace", "019a1b2c-3d4e-5f60-7182-93a4b5c6d7e8", "text-embedding-v3", CollectionPrefix + "_019a1b2c_3d4e_5f60_7182_93a4b5c6d7e8_text_embedding_v3"},
		{"alnum only", "workspace1", "text-embedding-v2", CollectionPrefix + "_workspace1_text_embedding_v2"},
		{"unsafe chars replaced", "my workspace/中文!", "text-embedding-v1", CollectionPrefix + "_my_workspace_____text_embedding_v1"},
		{"model chars sanitized", "workspace1", "my model/1", CollectionPrefix + "_workspace1_my_model_1"},
		{"empty model keeps legacy form", "workspace1", "", CollectionPrefix + "_workspace1_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollectionName("tenant-ignored", tc.workspaceID, tc.model); got != tc.want {
				t.Errorf("CollectionName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentLeaseRenewIntervalIsOneThirdOfTTL(t *testing.T) {
	// 续租间隔必须是租约的 1/3：单次续租失败仍有 2 次重试，同时把僵尸 runner
	// 的窗口压到 TTL/3（spec §6.7 D3）。
	if AgentExecutionLeaseRenewInterval*3 != AgentExecutionLeaseTTL {
		t.Fatalf("renew interval %v is not TTL/3 of lease %v",
			AgentExecutionLeaseRenewInterval, AgentExecutionLeaseTTL)
	}
}

func TestAgentViewerTimeoutIsShorterThanOrphanTimeout(t *testing.T) {
	// viewer 无心跳摘除必须显著早于孤儿取消，否则一个刚崩溃的订阅者会立刻
	// 触发孤儿计时（spec §7.1）。
	if AgentViewerTimeout >= AgentExecutionOrphanTimeout {
		t.Fatalf("viewer timeout %v must be shorter than orphan timeout %v",
			AgentViewerTimeout, AgentExecutionOrphanTimeout)
	}
}

func TestAgentStreamMaxLenCoversFullReplay(t *testing.T) {
	// MAXLEN 取 20000 而非 10000：F5 需要全量回放，裁剪造成的缺口会直接
	// 截断用户看到的答案（spec §6.5）。
	if AgentStreamMaxLen < 20000 {
		t.Fatalf("stream max len %d is too small for full replay", AgentStreamMaxLen)
	}
}
