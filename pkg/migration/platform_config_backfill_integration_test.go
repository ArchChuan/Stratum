//go:build integration

package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const platformCfgMigTestDBPrefix = "stratum_platformcfg_mig_"

// platformCfgMigTestSetup 创建独立测试库并返回连接池 + migrate 实例（未跑迁移，
// 由各用例控制 Migrate(n)/Up 的节奏，才能验证 043 backfill 读到的 platform_settings
// 内容是测试种下的）。
func platformCfgMigTestSetup(t *testing.T) (*pgxpool.Pool, *migrate.Migrate) {
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

	platformCfgMigDropStale(t, ctx, admin)

	dbName := platformCfgMigTestDBPrefix + fmt.Sprintf("%x", time.Now().UnixNano())
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

	migrateURL, err := driverURL(dbURL.String())
	if err != nil {
		t.Fatalf("normalize migrate URL: %v", err)
	}
	m, err := migrate.New("file://sql", migrateURL)
	if err != nil {
		t.Fatalf("init migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	return pool, m
}

func platformCfgMigDropStale(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1`, platformCfgMigTestDBPrefix+"%")
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

func platformCfgMigVersion1Snapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupKey string) map[string]json.RawMessage {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT v.snapshot FROM public.platform_config_versions v
		 WHERE v.group_key = $1 AND v.version_seq = 1`,
		groupKey).Scan(&raw); err != nil {
		t.Fatalf("read version-1 snapshot for %s: %v", groupKey, err)
	}
	snap := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot for %s: %v", groupKey, err)
	}
	return snap
}

// TestPlatformConfigMigration043BackfillEmptySettings 断言空 platform_settings
// 时 043 仍为每个分组生成 version_seq=1 的 published 空快照 + production/latest
// label：统一 label 语义，运行时 label 恒存在（空快照 = 全部 unset = 回退 default）。
func TestPlatformConfigMigration043BackfillEmptySettings(t *testing.T) {
	pool, m := platformCfgMigTestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// 建到 033（platform_settings 就位）但不种数据，再 034→043。
	if err := m.Migrate(33); err != nil {
		t.Fatalf("migrate to 33: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up to latest: %v", err)
	}

	for _, group := range []string{"agent", "memory", "evaluation", "trace"} {
		snap := platformCfgMigVersion1Snapshot(t, ctx, pool, group)
		if len(snap) != 0 {
			t.Fatalf("group %s version-1 snapshot = %v, want empty", group, snap)
		}
		var labels []string
		rows, err := pool.Query(ctx,
			`SELECT l.label FROM public.platform_config_labels l
			 JOIN public.platform_config_versions v ON v.id = l.version_id
			 WHERE v.group_key = $1 ORDER BY l.label`, group)
		if err != nil {
			t.Fatalf("query labels for %s: %v", group, err)
		}
		for rows.Next() {
			var label string
			if err := rows.Scan(&label); err != nil {
				rows.Close()
				t.Fatalf("scan label: %v", err)
			}
			labels = append(labels, label)
		}
		rows.Close()
		if len(labels) != 2 || labels[0] != "latest" || labels[1] != "production" {
			t.Fatalf("group %s labels = %v, want [latest production]", group, labels)
		}
	}
}

// TestPlatformConfigMigration043BackfillDropsDeprecatedKeys 断言 backfill 只投影
// registry 已知的 platform-scope key，按组归属落到对应快照；未注册/废弃 key
// （long_term_top_k、prompt.*）被丢弃，不携带残留。
//
// 注意 memory.* 的历史翻转：036 把 memory 键改为资源 scope 并 DELETE 了
// platform_settings 里的 memory.% 行，registry 后文又改回 ScopePlatform。因此
// 迁移到最新时 memory 组 backfill 恒为空快照（unset → 回退 default）——这是
// 036 已清行的历史事实，不是 backfill 缺陷。043 白名单含 memory.* 是前向兼容：
// 后续经版本化 UI 发布的 memory 值进 platform_config_versions，不依赖该表存量。
func TestPlatformConfigMigration043BackfillDropsDeprecatedKeys(t *testing.T) {
	pool, m := platformCfgMigTestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if err := m.Migrate(33); err != nil {
		t.Fatalf("migrate to 33: %v", err)
	}
	for _, row := range [][3]string{
		{"agent.factcheck.enabled", `true`, "admin-1"},
		{"evaluation.optimizer.temperature", `0.7`, "admin-1"},
		{"trace.capture_parameters", `true`, "admin-1"},
		{"memory.recall_top_k", `5`, "admin-1"}, // registry 已知，但 036 会清掉 memory.% 行
		{"long_term_top_k", `999`, "admin-1"},   // 已下线 key：必须丢弃
		{"prompt.foo", `"x"`, "admin-1"},        // 废弃 key：必须丢弃
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.platform_settings (key, value, updated_by) VALUES ($1, $2::jsonb, $3)`,
			row[0], row[1], row[2]); err != nil {
			t.Fatalf("seed %s: %v", row[0], err)
		}
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up to latest: %v", err)
	}

	agent := platformCfgMigVersion1Snapshot(t, ctx, pool, "agent")
	if string(agent["agent.factcheck.enabled"]) != `true` {
		t.Fatalf("agent snapshot missing known key: %v", agent)
	}
	if _, ok := agent["long_term_top_k"]; ok {
		t.Fatalf("deprecated key leaked into agent snapshot: %v", agent)
	}
	if _, ok := agent["prompt.foo"]; ok {
		t.Fatalf("deprecated key leaked into agent snapshot: %v", agent)
	}

	evaluation := platformCfgMigVersion1Snapshot(t, ctx, pool, "evaluation")
	if string(evaluation["evaluation.optimizer.temperature"]) != `0.7` {
		t.Fatalf("evaluation snapshot missing known key: %v", evaluation)
	}

	trace := platformCfgMigVersion1Snapshot(t, ctx, pool, "trace")
	if string(trace["trace.capture_parameters"]) != `true` {
		t.Fatalf("trace snapshot missing known key: %v", trace)
	}

	// memory: 036 删除 memory.% 行在前，043 backfill 后为空（历史事实）。
	memory := platformCfgMigVersion1Snapshot(t, ctx, pool, "memory")
	if len(memory) != 0 {
		t.Fatalf("memory snapshot = %v, want empty (036 cleared memory rows)", memory)
	}
}

// TestPlatformConfigMigration043IdempotentReapply 断言 043 全文件可重复执行：
// 全部 IF NOT EXISTS / ON CONFLICT DO NOTHING 防 force 后重跑产生重复 version-1
// 或重复 label。执行后版本号仍 43 且不 dirty。
func TestPlatformConfigMigration043IdempotentReapply(t *testing.T) {
	pool, m := platformCfgMigTestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if err := m.Up(); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
	sql, err := os.ReadFile("sql/043_platform_config_versions.up.sql")
	if err != nil {
		t.Fatalf("read 043 up: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("re-apply 043 up: %v", err)
	}

	var dup int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM (
			SELECT group_key, version_seq FROM public.platform_config_versions
			WHERE version_seq = 1 GROUP BY group_key, version_seq HAVING count(*) > 1
		) d`).Scan(&dup); err != nil {
		t.Fatalf("count duplicates: %v", err)
	}
	if dup != 0 {
		t.Fatalf("duplicate version-1 rows after re-apply: %d", dup)
	}

	var groupCount, labelCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.platform_config_versions WHERE version_seq = 1`).Scan(&groupCount); err != nil {
		t.Fatalf("count version-1: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.platform_config_labels`).Scan(&labelCount); err != nil {
		t.Fatalf("count labels: %v", err)
	}
	if groupCount != 4 || labelCount != 8 {
		t.Fatalf("after re-apply: version-1=%d labels=%d, want 4 and 8", groupCount, labelCount)
	}

	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("version check: %v", err)
	}
	if dirty || version != 43 {
		t.Fatalf("version = %d (dirty=%t), want 43 clean", version, dirty)
	}
}
