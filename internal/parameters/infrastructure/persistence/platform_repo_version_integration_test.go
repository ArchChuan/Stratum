//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/migration"
)

const platformCfgTestDBPrefix = "stratum_platformcfg_"

// platformCfgTestSetup 创建独立测试库并迁移到最新，返回连接池。
// 连接 STRATUM_TEST_POSTGRES_URL（未设则 skip）；CREATE/DROP DATABASE 在
// 指向 postgres 库的管理连接上、事务外执行，避免在待删库连接上 DROP 失败。
// 每用例独占数据库，禁止 t.Parallel()。
func platformCfgTestSetup(t *testing.T) *pgxpool.Pool {
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

	dropStale(t, ctx, admin)

	dbName := platformCfgTestDBPrefix + fmt.Sprintf("%x", time.Now().UnixNano())
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

	// 迁移到最新：同时完成 043 的 seed + backfill（每分组 version-1 published）。
	sqlDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "pkg", "migration", "sql"))
	if err != nil {
		t.Fatalf("resolve migration dir: %v", err)
	}
	if err := migration.RunPublicSchema(dbURL.String(), sqlDir, zap.NewNop()); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}
	return pool
}

// dropStale 删除历史崩溃残留的测试库（仅本前缀，由本测试独占）。
func dropStale(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1`, platformCfgTestDBPrefix+"%")
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

func mustSnapshot(t *testing.T, repo *PlatformRepository, groupKey string) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	snap, err := repo.GetSnapshot(ctx, groupKey)
	if err != nil {
		t.Fatalf("GetSnapshot(%s): %v", groupKey, err)
	}
	return snap
}

func mustDraft(t *testing.T, repo *PlatformRepository, groupKey string, snapshot map[string]json.RawMessage, message string) port.PlatformVersion {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	v, err := repo.CreateDraft(ctx, groupKey, snapshot, message, "admin-1")
	if err != nil {
		t.Fatalf("CreateDraft(%s): %v", groupKey, err)
	}
	return v
}

func mustPublish(t *testing.T, repo *PlatformRepository, groupKey string, versionID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := repo.Publish(ctx, groupKey, versionID, "admin-1"); err != nil {
		t.Fatalf("Publish(%s, %d): %v", groupKey, versionID, err)
	}
}

func mustListVersions(t *testing.T, repo *PlatformRepository, groupKey string) []port.PlatformVersion {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	versions, err := repo.ListVersions(ctx, groupKey)
	if err != nil {
		t.Fatalf("ListVersions(%s): %v", groupKey, err)
	}
	return versions
}

// TestPgPlatformVersionPublishBaseChainAndRollback 走完整事务语义：
// draft 未发布不影响 production 快照；Publish 一次性挪 label + 记录
// base_version_id（= Publish 时 production 所指版本）；Rollback 挪 label
// 回历史版本、不产新版本、不改快照。
func TestPgPlatformVersionPublishBaseChainAndRollback(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)

	// 043 backfill 已建 version-1 published（空快照），production/latest 指向它。
	initial := mustListVersions(t, repo, "trace")
	if len(initial) != 1 || initial[0].VersionSeq != 1 || initial[0].Status != "published" {
		t.Fatalf("backfill = %+v, want single published seq=1", initial)
	}
	if snap := mustSnapshot(t, repo, "trace"); len(snap) != 0 {
		t.Fatalf("backfill snapshot = %v, want empty", snap)
	}

	// draft 未发布：production 快照保持不变。
	v2 := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`true`)}, "enable")
	if v2.Status != "draft" || v2.VersionSeq != 2 {
		t.Fatalf("draft = %+v, want seq=2 draft", v2)
	}
	if snap := mustSnapshot(t, repo, "trace"); len(snap) != 0 {
		t.Fatalf("production must be untouched before publish, got %v", snap)
	}

	mustPublish(t, repo, "trace", v2.ID)
	if snap := mustSnapshot(t, repo, "trace"); string(snap["trace.capture_parameters"]) != `true` {
		t.Fatalf("snapshot after publish = %v, want capture_parameters=true", snap)
	}

	// 第二个版本：base_version_id 必须 = 上一个 production（v2）。
	v3 := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`false`)}, "disable")
	mustPublish(t, repo, "trace", v3.ID)
	versions := mustListVersions(t, repo, "trace")
	if len(versions) != 3 {
		t.Fatalf("versions = %d, want 3", len(versions))
	}
	var found *port.PlatformVersion
	for i := range versions {
		if versions[i].VersionSeq == 3 {
			found = &versions[i]
		}
	}
	if found == nil || found.BaseVersion == nil || *found.BaseVersion != v2.ID {
		t.Fatalf("v3 base_version_id = %+v, want %d", found, v2.ID)
	}

	// Rollback 到 v2：production 回到 v2 快照，不产新版本，版本状态不变。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := repo.Rollback(ctx, "trace", v2.ID, "admin-1"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if snap := mustSnapshot(t, repo, "trace"); string(snap["trace.capture_parameters"]) != `true` {
		t.Fatalf("snapshot after rollback = %v, want capture_parameters=true", snap)
	}
	after := mustListVersions(t, repo, "trace")
	if len(after) != 3 {
		t.Fatalf("rollback must not create a version, got %d", len(after))
	}
	for _, v := range after {
		if v.Status != "published" {
			t.Fatalf("rollback must not change version status, got %+v", v)
		}
	}
}

// TestPgPlatformVersionStateMachineErrors 断言真实 DB 上的状态机错误：
// 未知版本 / 重复发布 / 回滚到 draft / 非法分组。
func TestPgPlatformVersionStateMachineErrors(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	if err := repo.Publish(ctx, "trace", 9999, "admin-1"); !errors.Is(err, domain.ErrVersionNotFound) {
		t.Fatalf("publish unknown = %v, want ErrVersionNotFound", err)
	}
	if err := repo.Rollback(ctx, "trace", 9999, "admin-1"); !errors.Is(err, domain.ErrVersionNotFound) {
		t.Fatalf("rollback unknown = %v, want ErrVersionNotFound", err)
	}

	v := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`true`)}, "m")
	mustPublish(t, repo, "trace", v.ID)
	if err := repo.Publish(ctx, "trace", v.ID, "admin-1"); !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("re-publish = %v, want ErrVersionNotDraft", err)
	}

	draft := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`false`)}, "m2")
	if err := repo.Rollback(ctx, "trace", draft.ID, "admin-1"); !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("rollback draft = %v, want ErrVersionNotPublished", err)
	}

	if _, err := repo.CreateDraft(ctx, "bogus", map[string]json.RawMessage{}, "m", "admin-1"); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("create draft in unknown group = %v, want ErrGroupNotFound", err)
	}

	// H4：archived 目标是死状态——发布只接受 draft、回滚只接受 published，
	// 归档版本两者都必须拒绝（防止把 production 指回已修剪的历史）。
	arch := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`true`)}, "to-archive")
	mustPublish(t, repo, "trace", arch.ID)
	if _, err := pool.Exec(ctx, `UPDATE public.platform_config_versions SET status = 'archived' WHERE id = $1`, arch.ID); err != nil {
		t.Fatalf("mark archived: %v", err)
	}
	if err := repo.Publish(ctx, "trace", arch.ID, "admin-1"); !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("publish archived = %v, want ErrVersionNotDraft", err)
	}
	if err := repo.Rollback(ctx, "trace", arch.ID, "admin-1"); !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("rollback archived = %v, want ErrVersionNotPublished", err)
	}
}

// TestPgPlatformVersionConcurrentDrafts 断言并发 CreateDraft 由 per-group
// FOR UPDATE 串行化：两个 goroutine 同时创建 draft 必须得到不同且连续的
// version_seq（backfill 后 max=1 → 并发产出 {2,3}），不撞 UNIQUE 约束。
func TestPgPlatformVersionConcurrentDrafts(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)

	seqs := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := mustDraft(t, repo, "agent", map[string]json.RawMessage{"agent.factcheck.enabled": json.RawMessage(`true`)}, "race")
			seqs <- v.VersionSeq
		}()
	}
	wg.Wait()
	close(seqs)

	got := make([]int, 0, 2)
	for s := range seqs {
		got = append(got, s)
	}
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("concurrent seqs = %v, want two distinct values", got)
	}
	// 连续：{2,3}（backfill 占 seq=1）。
	sort.Ints(got)
	if got[0] != 2 || got[1] != 3 {
		t.Fatalf("concurrent seqs = %v, want [2 3]", got)
	}
}

// TestPgPlatformVersionRetentionTrim 断言保留上限：非 draft 版本数超过
// MaxPlatformConfigVersionsPerGroup 后，最旧 published 自动 archive，
// 新建 draft 不阻断。backfill 占 seq=1，批量插入 seq=2..99 凑到 100，
// 发布第 101 个时 seq=1 被 archive。
func TestPgPlatformVersionRetentionTrim(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// backfill seq=1 published + 98 个批量 published（seq 2..99）= 100 非 draft。
	for seq := 2; seq <= 99; seq++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.platform_config_versions
			   (group_key, version_seq, status, snapshot, message, created_by)
			 VALUES ('trace', $1, 'published', '{}'::jsonb, 'bulk', 'system')`,
			seq); err != nil {
			t.Fatalf("bulk insert seq %d: %v", seq, err)
		}
	}

	// 第 100 个（seq=100）：over=0，不修剪。
	release := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`true`)}, "release")
	mustPublish(t, repo, "trace", release.ID)
	atLimit := mustListVersions(t, repo, "trace")
	if len(atLimit) != constants.MaxPlatformConfigVersionsPerGroup {
		t.Fatalf("at limit = %d versions, want %d", len(atLimit), constants.MaxPlatformConfigVersionsPerGroup)
	}
	if statusOf(atLimit, 1) != "published" {
		t.Fatalf("seq=1 must still be published at limit, got %q", statusOf(atLimit, 1))
	}

	// 第 101 个（seq=101）：over=1，最旧 published（seq=1）archive。
	overflow := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`false`)}, "overflow")
	mustPublish(t, repo, "trace", overflow.ID)
	after := mustListVersions(t, repo, "trace")
	if statusOf(after, 1) != "archived" {
		t.Fatalf("seq=1 must be archived after overflow, got %q", statusOf(after, 1))
	}
	if statusOf(after, 2) != "published" {
		t.Fatalf("seq=2 must survive trim, got %q", statusOf(after, 2))
	}
	if statusOf(after, 101) != "published" {
		t.Fatalf("newest must stay published, got %q", statusOf(after, 101))
	}

	// H5：trim（archive）只改版本表状态，append-only 审计行不随之删除——版本
	// 修剪丢证的补救由 platform_resource_change_audits 独立承载。本例只有
	// release/overflow 两次走 Publish 路径（backfill 与 bulk INSERT 不经审计），
	// archive seq=1 后审计行数必须原样保留。
	audits := platformAuditRows(t, pool, "trace")
	if len(audits) != 2 {
		t.Fatalf("audit rows after trim = %d, want 2 (archive must not delete audit rows)", len(audits))
	}
	for _, a := range audits {
		if a.Operation != auditdomain.ChangeOpPublish {
			t.Fatalf("audit operation = %s, want %s", a.Operation, auditdomain.ChangeOpPublish)
		}
	}
}

func statusOf(versions []port.PlatformVersion, seq int) string {
	for _, v := range versions {
		if v.VersionSeq == seq {
			return v.Status
		}
	}
	return ""
}

// platformAuditRows 按 group_key 拉取平台配置审计行，断言投影与 actor。
// 验证点：发布/回滚写 platform_resource_change_audits（append-only），
// Before/After 是脱敏 JSON 投影、actor 归因到操作者。
type platformAuditRow struct {
	Operation string
	ActorID   string
	Before    json.RawMessage
	After     json.RawMessage
}

func platformAuditRows(t *testing.T, pool *pgxpool.Pool, groupKey string) []platformAuditRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	rows, err := pool.Query(ctx,
		`SELECT operation, actor_id, before_projection, after_projection
		 FROM public.platform_resource_change_audits
		 WHERE resource_kind = $1 AND resource_id = $2
		 ORDER BY created_at ASC, id ASC`,
		auditdomain.ResourceKindPlatformConfig, groupKey)
	if err != nil {
		t.Fatalf("query platform audits for %s: %v", groupKey, err)
	}
	defer rows.Close()
	var out []platformAuditRow
	for rows.Next() {
		var (
			r      platformAuditRow
			before []byte
			after  []byte
		)
		if err := rows.Scan(&r.Operation, &r.ActorID, &before, &after); err != nil {
			t.Fatalf("scan platform audit: %v", err)
		}
		r.Before = json.RawMessage(before)
		r.After = json.RawMessage(after)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate platform audits: %v", err)
	}
	return out
}

// TestPgPlatformVersionAuditTrail 断言 Publish/Rollback 与审计行同事务落库：
// 首次发布 Before='{}'（无 production）；第二次发布 Before=上次快照；回滚
// Before=回滚前快照、After=目标快照；失败路径（回滚到 draft）不产生审计残留。
func TestPgPlatformVersionAuditTrail(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)

	// 首次发布：Before 恒 '{}'（backfill version-1 是空快照，production 语义
	// 上「无有效配置」），After = 本次快照。
	v2 := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`true`)}, "enable")
	mustPublish(t, repo, "trace", v2.ID)

	// 第二次发布：Before = 上次 production 快照（capture_parameters=true）。
	v3 := mustDraft(t, repo, "trace", map[string]json.RawMessage{"trace.capture_parameters": json.RawMessage(`false`)}, "disable")
	mustPublish(t, repo, "trace", v3.ID)

	// 回滚到 v2：Before = 回滚前 production（false），After = 目标（true）。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := repo.Rollback(ctx, "trace", v2.ID, "admin-1"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	rows := platformAuditRows(t, pool, "trace")
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3 (publish, publish, rollback)", len(rows))
	}

	// publish #1: Before='{}', After=true。
	if rows[0].Operation != auditdomain.ChangeOpPublish || rows[0].ActorID != "admin-1" {
		t.Fatalf("audit[0] = %+v, want publish by admin-1", rows[0])
	}
	if string(rows[0].Before) != `{}` {
		t.Fatalf("audit[0] Before = %s, want {}", rows[0].Before)
	}
	var a1 map[string]json.RawMessage
	if err := json.Unmarshal(rows[0].After, &a1); err != nil {
		t.Fatalf("audit[0] After decode: %v", err)
	}
	if string(a1["trace.capture_parameters"]) != `true` {
		t.Fatalf("audit[0] After = %s, want capture_parameters=true", rows[0].After)
	}

	// publish #2: Before=true, After=false。
	if rows[1].Operation != auditdomain.ChangeOpPublish || rows[1].ActorID != "admin-1" {
		t.Fatalf("audit[1] = %+v, want publish by admin-1", rows[1])
	}
	var b2, a2 map[string]json.RawMessage
	if err := json.Unmarshal(rows[1].Before, &b2); err != nil || string(b2["trace.capture_parameters"]) != `true` {
		t.Fatalf("audit[1] Before = %s, want true", rows[1].Before)
	}
	if err := json.Unmarshal(rows[1].After, &a2); err != nil || string(a2["trace.capture_parameters"]) != `false` {
		t.Fatalf("audit[1] After = %s, want false", rows[1].After)
	}

	// rollback: Before=false（回滚前）, After=true（目标版本）。
	if rows[2].Operation != auditdomain.ChangeOpRollback || rows[2].ActorID != "admin-1" {
		t.Fatalf("audit[2] = %+v, want rollback by admin-1", rows[2])
	}
	var b3, a3 map[string]json.RawMessage
	if err := json.Unmarshal(rows[2].Before, &b3); err != nil || string(b3["trace.capture_parameters"]) != `false` {
		t.Fatalf("audit[2] Before = %s, want false", rows[2].Before)
	}
	if err := json.Unmarshal(rows[2].After, &a3); err != nil || string(a3["trace.capture_parameters"]) != `true` {
		t.Fatalf("audit[2] After = %s, want true", rows[2].After)
	}
}

// TestPgPlatformVersionAuditFailureIsAtomic 断言失败路径不产生审计残留：
// rollback 到 draft 版本在校验阶段即失败，整个事务回滚，无 ChangeOpRollback 行。
func TestPgPlatformVersionAuditFailureIsAtomic(t *testing.T) {
	pool := platformCfgTestSetup(t)
	repo := NewPlatformRepository(pool)

	v2 := mustDraft(t, repo, "agent", map[string]json.RawMessage{"agent.factcheck.enabled": json.RawMessage(`true`)}, "m")
	mustPublish(t, repo, "agent", v2.ID)

	draft := mustDraft(t, repo, "agent", map[string]json.RawMessage{"agent.factcheck.enabled": json.RawMessage(`false`)}, "m2")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := repo.Rollback(ctx, "agent", draft.ID, "admin-1"); !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("rollback draft = %v, want ErrVersionNotPublished", err)
	}

	rows := platformAuditRows(t, pool, "agent")
	if len(rows) != 1 || rows[0].Operation != auditdomain.ChangeOpPublish {
		t.Fatalf("audit rows = %+v, want single publish (failed rollback must not leave a row)", rows)
	}
}
