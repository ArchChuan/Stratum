package infrastructure_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// newSeedProviderRepo 创建共享 pool 的 provider repo，并注册该 provider ID
// 的 public.providers 行清理。public 表全局共享，测试间靠唯一 ID + cleanup 隔离。
func newSeedProviderRepo(t *testing.T) (*pgxpool.Pool, *infrastructure.PgProviderRepo, string) {
	pool := postgrestest.NewPool(t)
	repo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	id := fmt.Sprintf("test-prov-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.providers WHERE id=$1`, id)
	})
	return pool, repo, id
}

func TestPgProviderRepo_CRUD(t *testing.T) {
	pool, repo, providerID := newSeedProviderRepo(t)
	ctx := context.Background()

	p := &domain.Provider{
		ID:      providerID,
		Name:    "test-qwen",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}

	// Create
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Create 必须把 API key 以密文落库：直接读 public 原始行验证存储值不是明文，
	// 且解密后还原明文。
	var storedKey string
	if err := pool.QueryRow(ctx,
		`SELECT api_key FROM public.providers WHERE id=$1`, p.ID,
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
	got, err := repo.Get(ctx, p.ID)
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
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	// Update
	p.Name = "test-qwen-2"
	p.Kind = domain.ProviderAnthropic
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = repo.Get(ctx, p.ID)
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
	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.Get(ctx, p.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPgProviderRepo_GetNotFound(t *testing.T) {
	_, repo, _ := newSeedProviderRepo(t)
	ctx := context.Background()

	_, err := repo.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestPgProviderRepo_DeleteNotFound(t *testing.T) {
	_, repo, _ := newSeedProviderRepo(t)
	ctx := context.Background()

	err := repo.Delete(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent provider")
	}
}

func TestPgProviderRepo_ListEmpty(t *testing.T) {
	_, repo, _ := newSeedProviderRepo(t)
	ctx := context.Background()

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list len: got %d, want 0", len(list))
	}
}

// TestPgProviderRepo_LegacyPlaintextReadable 验证双读兼容：加密上线前的存量
// 明文经 Get 返回原值（恢复运行时可用）、出现在 List 中（管理页可见），
// 写路径仍由 Create/Update 加密。
func TestPgProviderRepo_LegacyPlaintextReadable(t *testing.T) {
	pool, repo, providerID := newSeedProviderRepo(t)
	ctx := context.Background()

	p := &domain.Provider{
		ID: providerID, Name: "legacy", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://legacy.example.com", APIKey: "sk-legacy", Enabled: true,
	}
	// 直接以明文写入（模拟历史数据）。
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.providers
		 (id, name, kind, base_url, api_key, default_model, enabled)
		 VALUES ($1,$2,$3,$4,$5,'',true)`,
		p.ID, p.Name, string(p.Kind), p.BaseURL, p.APIKey); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get legacy plaintext provider must succeed (dual-read): %v", err)
	}
	if got.APIKey != "sk-legacy" {
		t.Fatalf("get api key: got %q, want legacy plaintext as-is", got.APIKey)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list with legacy plaintext entry must not fail: %v", err)
	}
	if len(list) != 1 || list[0].APIKey != "sk-legacy" {
		t.Fatalf("legacy plaintext provider must be listed with readable key, got: %+v", list)
	}
}

// TestPgProviderRepo_List_SkipsCorruptEntry 验证一条损坏密文不影响列表整体：
// List 跳过解密失败的条目并返回其余 provider，管理页的编辑/删除入口保持可用。
func TestPgProviderRepo_List_SkipsCorruptEntry(t *testing.T) {
	pool, repo, _ := newSeedProviderRepo(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	insert := func(id, apiKey string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.providers
			 (id, name, kind, base_url, api_key, default_model, enabled)
			 VALUES ($1,$2,$3,$4,$5,'',true)`,
			id, id, string(domain.ProviderOpenAICompat), "https://"+id+".example.com", apiKey); err != nil {
			t.Fatalf("seed row %s: %v", id, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM public.providers WHERE id=$1`, id)
		})
	}
	// 2 条损坏密文（payload 非法 base64 / 合法 base64 但 key 不匹配）+ 2 条正常记录。
	otherKey := [32]byte{8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
		8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8}
	wrongKeyCt, err := crypto.EncryptSecret(otherKey, "sk-from-another-key")
	if err != nil {
		t.Fatalf("encrypt wrong-key fixture: %v", err)
	}
	corrupt1, corrupt2 := fmt.Sprintf("corrupt-1-%d", suffix), fmt.Sprintf("corrupt-2-%d", suffix)
	insert(corrupt1, "enc:v1:not-valid-ciphertext!!!")
	insert(corrupt2, wrongKeyCt)
	for _, id := range []string{fmt.Sprintf("good-1-%d", suffix), fmt.Sprintf("good-2-%d", suffix)} {
		enc, err := crypto.EncryptSecret(testRepoKey, "sk-"+id)
		if err != nil {
			t.Fatalf("encrypt %s: %v", id, err)
		}
		insert(id, enc)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list with corrupt entry must not fail wholesale: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len: got %d, want 2 (corrupt entries skipped)", len(list))
	}
	for _, p := range list {
		if p.ID == corrupt1 || p.ID == corrupt2 {
			t.Fatalf("corrupt entry must be excluded from result: %+v", p)
		}
		if p.APIKey == "" || strings.HasPrefix(p.APIKey, "enc:v1:") {
			t.Fatalf("provider %s api key not decrypted: %q", p.ID, p.APIKey)
		}
	}

	// Get 单条访问仍 fail closed：与 List 的可用性优先策略不同。
	for _, id := range []string{corrupt1, corrupt2} {
		if _, err := repo.Get(ctx, id); err == nil {
			t.Fatalf("Get on corrupt entry %s must stay fail closed", id)
		}
	}
}
