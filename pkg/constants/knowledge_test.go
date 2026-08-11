package constants

import "testing"

func TestCollectionNameEncodesModel(t *testing.T) {
	wsID := "9a1c3b2d-0000-4000-8000-000000000001"
	// workspaceID 和 model 都经 SanitizeMilvusName：连字符也被替换为下划线。
	if got := CollectionName("", wsID, "text-embedding-v3"); got != "kb_"+SanitizeMilvusName(wsID)+"_text_embedding_v3" {
		t.Errorf("CollectionName = %q", got)
	}
	if got := SanitizeMilvusName("a-b c.d/e"); got != "a_b_c_d_e" {
		t.Errorf("SanitizeMilvusName = %q", got)
	}
}

// TestCollectionLegacyNamePin pins the no-model-suffix legacy collection name
// (pre-upgrade data). Writes always use CollectionName; reads and deletes
// fall back to this name when the model-suffixed collection is absent.
func TestCollectionLegacyNamePin(t *testing.T) {
	wsID := "9a1c3b2d-0000-4000-8000-000000000001"
	if got := CollectionLegacyName("", wsID); got != "kb_"+SanitizeMilvusName(wsID) {
		t.Errorf("CollectionLegacyName = %q, want %q", got, "kb_"+SanitizeMilvusName(wsID))
	}
	if got := CollectionLegacyName("", "my workspace/中文!"); got != "kb_my_workspace____" {
		t.Errorf("CollectionLegacyName sanitizes = %q", got)
	}
}
