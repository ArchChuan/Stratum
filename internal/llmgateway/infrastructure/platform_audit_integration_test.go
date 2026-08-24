//go:build integration

package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/migration"
)

const platformAuditTestDBPrefix = "stratum_platformaudit_"

// platformAuditTestSetup 创建独立测试库并迁移到最新，返回连接池。模型目录
// （public.models/providers）与平台审计表（public.platform_resource_change_audits）
// 均在 035/039 迁移就位。
func platformAuditTestSetup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	baseURL := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	adminURL := *parsed
	adminURL.Path = "/postgres"
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	dropStale := func() {
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1`, platformAuditTestDBPrefix+"%")
		if err != nil {
			t.Fatalf("list stale databases: %v", err)
		}
		var stale []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("scan stale database: %v", err)
			}
			stale = append(stale, name)
		}
		rows.Close()
		for _, name := range stale {
			if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
				t.Fatalf("drop stale database %s: %v", name, err)
			}
		}
	}
	dropStale()

	dbName := platformAuditTestDBPrefix + fmt.Sprintf("%x", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
	})

	dbURL := *parsed
	dbURL.Path = "/" + dbName
	pool, err := pgxpool.New(ctx, dbURL.String())
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sqlDir, err := filepath.Abs(filepath.Join("..", "..", "..", "pkg", "migration", "sql"))
	if err != nil {
		t.Fatalf("resolve migration dir: %v", err)
	}
	if err := migration.RunPublicSchema(dbURL.String(), sqlDir, zap.NewNop()); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}
	return pool
}

// TestPgModelUpdatePlatformAuditRow 断言 model/provider 变更仍落平台审计：
// UpdatePlatform 在同一个 public 事务内经 insertPlatformAuditTx 写
// platform_resource_change_audits 行，actor_tenant_id/before/after 投影完整。
// 这是 insertPlatformAuditTx 写入链路的真实 DB 断言（P2 要求补，不能写"复用既有断言"）。
func TestPgModelUpdatePlatformAuditRow(t *testing.T) {
	pool := platformAuditTestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.providers (id, name, kind, base_url, api_key) VALUES ('p1','acme','openai','https://api.example.com','sk-test')`); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.models (id, provider_id, name, display_name) VALUES ('m1','p1','gpt-4o','GPT-4o')`); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	repo := NewPgModelRepo(pool)
	before := json.RawMessage(`{"display_name":"GPT-4o","enabled":true}`)
	after := json.RawMessage(`{"display_name":"GPT-4o (ops)","enabled":true}`)
	if err := repo.UpdatePlatform(ctx, &domain.Model{
		ID: "m1", ProviderID: "p1", Name: "gpt-4o", DisplayName: "GPT-4o (ops)",
		FallbackCandidates: []string{},
	}, "tenant-42", &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindModel,
		ResourceID:   "m1",
		Operation:    auditdomain.ChangeOpUpdate,
		ActorID:      "admin-1",
		Before:       before,
		After:        after,
	}); err != nil {
		t.Fatalf("UpdatePlatform: %v", err)
	}

	var (
		operation, resourceKind, resourceID, actorID, actorTenant string
		gotBefore, gotAfter                                       []byte
	)
	err := pool.QueryRow(ctx,
		`SELECT operation, resource_kind, resource_id, actor_id, COALESCE(actor_tenant_id,''),
		        before_projection, after_projection
		 FROM public.platform_resource_change_audits
		 WHERE resource_kind = $1 AND resource_id = $2`,
		auditdomain.ResourceKindModel, "m1",
	).Scan(&operation, &resourceKind, &resourceID, &actorID, &actorTenant, &gotBefore, &gotAfter)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if operation != auditdomain.ChangeOpUpdate || resourceKind != auditdomain.ResourceKindModel ||
		resourceID != "m1" || actorID != "admin-1" || actorTenant != "tenant-42" {
		t.Fatalf("audit row = (%s,%s,%s,%s,tenant=%s), want (update,model,m1,admin-1,tenant-42)",
			operation, resourceKind, resourceID, actorID, actorTenant)
	}
	var bProj, aProj map[string]json.RawMessage
	if err := json.Unmarshal(gotBefore, &bProj); err != nil {
		t.Fatalf("decode before projection: %v", err)
	}
	if string(bProj["display_name"]) != `"GPT-4o"` || string(bProj["enabled"]) != `true` {
		t.Fatalf("before projection = %s, want {display_name GPT-4o, enabled true}", gotBefore)
	}
	if err := json.Unmarshal(gotAfter, &aProj); err != nil {
		t.Fatalf("decode after projection: %v", err)
	}
	if string(aProj["display_name"]) != `"GPT-4o (ops)"` || string(aProj["enabled"]) != `true` {
		t.Fatalf("after projection = %s, want {display_name GPT-4o (ops), enabled true}", gotAfter)
	}
}

// TestPgModelUpdatePlatformAuditFailureNoRow 断言 UpdatePlatform 的目标行不存在时
// 整个事务回滚，不写审计残留行（失败路径 fail-closed，无半提交证据）。
func TestPgModelUpdatePlatformAuditFailureNoRow(t *testing.T) {
	pool := platformAuditTestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	repo := NewPgModelRepo(pool)
	err := repo.UpdatePlatform(ctx, &domain.Model{
		ID: "missing", ProviderID: "p1", Name: "x",
	}, "tenant-42", &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindModel,
		ResourceID:   "missing",
		Operation:    auditdomain.ChangeOpUpdate,
		ActorID:      "admin-1",
	})
	if err == nil {
		t.Fatalf("UpdatePlatform(missing) = nil, want error")
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.platform_resource_change_audits WHERE resource_id = 'missing'`,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("audit rows for missing model = %d, want 0 (failed tx must leave no evidence)", n)
	}
}
