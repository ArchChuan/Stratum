package infrastructure_test

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

// testRepoKey 与 infrastructure 内部测试共用同一固定测试密钥。
var testRepoKey = [32]byte{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
}

func TestPgProviderRepo_CRUD(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	p := &domain.Provider{
		ID:      "test-prov-1",
		Name:    "test-qwen",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}

	// Create
	if err := repo.Create(ctx, tenantID, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Create 必须把 API key 以密文落库：直接读 tenant schema 原始行验证
	// 存储值不是明文，且解密后还原明文。
	var storedKey string
	if err := pool.QueryRow(ctx,
		`SELECT api_key FROM "tenant_`+tenantID+`".providers WHERE id=$1`, p.ID,
	).Scan(&storedKey); err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	if storedKey == p.APIKey || !strings.HasPrefix(storedKey, "enc:v1:") {
		t.Fatalf("api key stored in plaintext: got %q, want enc:v1: ciphertext", storedKey)
	}
	plain, err := crypto.DecryptSecret(testRepoKey, storedKey)
	if err != nil {
		t.Fatalf("decrypt stored key: %v", err)
	}
	if plain != p.APIKey {
		t.Fatalf("round-trip: got %q, want %q", plain, p.APIKey)
	}

	// Get
	got, err := repo.Get(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != p.Name {
		t.Errorf("name: got %q, want %q", got.Name, p.Name)
	}
	if got.Kind != p.Kind {
		t.Errorf("kind: got %q, want %q", got.Kind, p.Kind)
	}
	if got.APIKey != p.APIKey {
		t.Errorf("api key after decrypt: got %q, want %q", got.APIKey, p.APIKey)
	}

	// List
	list, err := repo.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	// Update
	p.Name = "test-qwen-2"
	p.Kind = domain.ProviderAnthropic
	if err := repo.Update(ctx, tenantID, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = repo.Get(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "test-qwen-2" {
		t.Errorf("name after update: got %q, want %q", got.Name, "test-qwen-2")
	}
	if got.Kind != domain.ProviderAnthropic {
		t.Errorf("kind after update: got %q, want %q", got.Kind, domain.ProviderAnthropic)
	}
	if !got.UpdatedAt.After(got.CreatedAt) {
		t.Error("expected updated_at to advance after update")
	}

	// Delete
	if err := repo.Delete(ctx, tenantID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.Get(ctx, tenantID, p.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPgProviderRepo_GetNotFound(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	_, err := repo.Get(ctx, tenantID, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestPgProviderRepo_DeleteNotFound(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	// Delete nonexistent should succeed (no rows affected is not an error in this implementation)
	err := repo.Delete(ctx, tenantID, "nonexistent")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent provider")
	}
}

func TestPgProviderRepo_ListEmpty(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	list, err := repo.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list len: got %d, want 0", len(list))
	}
}

// TestPgProviderRepo_LegacyPlaintextFailsClosed 验证加密上线前的存量明文：
// 读取必须 fail closed（返回"请重新保存"错误），禁止把明文当可用 key 返回。
func TestPgProviderRepo_LegacyPlaintextFailsClosed(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	p := &domain.Provider{
		ID: "legacy-provider", Name: "legacy", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://legacy.example.com", APIKey: "sk-legacy", Enabled: true,
	}
	// 直接以明文写入（模拟历史数据）。
	if _, err := pool.Exec(ctx,
		`INSERT INTO "tenant_`+tenantID+`".providers
		 (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,'',true)`,
		p.ID, tenantID, p.Name, string(p.Kind), p.BaseURL, p.APIKey); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if _, err := repo.Get(ctx, tenantID, p.ID); err == nil {
		t.Fatal("expected fail-closed error for legacy plaintext api key")
	} else if !strings.Contains(err.Error(), "请重新保存") {
		t.Fatalf("expected re-save hint in error, got: %v", err)
	}
}

// TestPgProviderRepo_List_SkipsCorruptEntry 验证一条损坏密文不影响列表整体：
// List 跳过解密失败的条目并返回其余 provider，管理页的编辑/删除入口保持可用。
func TestPgProviderRepo_List_SkipsCorruptEntry(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	ctx := context.Background()

	insert := func(id, apiKey string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO "tenant_`+tenantID+`".providers
			 (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6,'',true)`,
			id, tenantID, id, string(domain.ProviderOpenAICompat), "https://"+id+".example.com", apiKey); err != nil {
			t.Fatalf("seed row %s: %v", id, err)
		}
	}
	// 1 条损坏密文 + 2 条正常（经 repo 加密）记录。
	insert("corrupt-1", "enc:v1:not-valid-ciphertext!!!")
	for _, id := range []string{"good-1", "good-2"} {
		enc, err := crypto.EncryptSecret(testRepoKey, "sk-"+id)
		if err != nil {
			t.Fatalf("encrypt %s: %v", id, err)
		}
		insert(id, enc)
	}

	list, err := repo.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list with corrupt entry must not fail wholesale: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len: got %d, want 2 (corrupt entry skipped)", len(list))
	}
	for _, p := range list {
		if p.ID == "corrupt-1" {
			t.Fatalf("corrupt entry must be excluded from result: %+v", p)
		}
		if p.APIKey == "" || strings.HasPrefix(p.APIKey, "enc:v1:") {
			t.Fatalf("provider %s api key not decrypted: %q", p.ID, p.APIKey)
		}
	}

	// Get 单条访问仍 fail closed：与 List 的可用性优先策略不同。
	if _, err := repo.Get(ctx, tenantID, "corrupt-1"); err == nil {
		t.Fatal("Get on corrupt entry must stay fail closed")
	}
}
