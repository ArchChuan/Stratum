package migration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rollbackTestDBPrefix = "stratum_rollback_"

// TestPublicSchemaMigrationsRollBackStepwiseToZero 对全部编号迁移执行真实的
// Up → 逆序 Down → 再 Up 全链路,验证 down 文件可执行、顺序可逆、零版本干净。
//
// 连接 STRATUM_TEST_POSTGRES_URL(未设则 skip,与 pkg/storage/postgres 集成测试同约定),
// 每次运行 CREATE 独立数据库(需 superuser/CREATEDB;CI service 与本地 compose 均满足),
// 全程只作用于自己的库,可与 pkg/storage/postgres 包并行执行。
// 本测试独占数据库,禁止 t.Parallel()。
func TestPublicSchemaMigrationsRollBackStepwiseToZero(t *testing.T) {
	baseURL := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	dbURL := rollbackTestCreateDB(t, baseURL)
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	latest := rollbackTestLatestVersion(t)

	// ── 阶段 1: Up 到最新,冒烟断言首尾迁移的表就位(001 tenants/users,033 platform_settings) ──
	migrateURL, err := driverURL(dbURL)
	if err != nil {
		t.Fatalf("normalize migrate URL: %v", err)
	}
	m, err := migrate.New("file://sql", migrateURL)
	if err != nil {
		t.Fatalf("init migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Up(); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
	assertRollbackVersion(t, m, latest)
	for _, table := range []string{"tenants", "users", "platform_settings"} {
		if !rollbackTestHasTable(t, ctx, pool, table) {
			t.Fatalf("table %s missing after up to latest", table)
		}
	}

	// ── 阶段 2: 逆序逐版本回滚到 1,每步断言版本严格递减 ──
	for step := 0; step < int(latest)-1; step++ {
		want := latest - uint(step) - 1
		if err := m.Steps(-1); err != nil {
			t.Fatalf("rollback to %d: %v", want, err)
		}
		assertRollbackVersion(t, m, want)
	}

	// ── 阶段 3: 最后一降 1 → 0:版本行被删,Version() 转 ErrNilVersion ──
	if err := m.Steps(-1); err != nil {
		t.Fatalf("rollback to zero: %v", err)
	}
	if _, _, err := m.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("version after rollback to zero = %v, want migrate.ErrNilVersion", err)
	}
	for _, table := range []string{"tenants", "users", "platform_settings"} {
		if rollbackTestHasTable(t, ctx, pool, table) {
			t.Fatalf("table %s still exists after rollback to zero", table)
		}
	}
	// 零版本上再降:readDown 对 from==-1 返回 os.ErrNotExist
	if err := m.Steps(-1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Steps(-1) at zero = %v, want os.ErrNotExist", err)
	}

	// ── 阶段 4: 从零重放 Up(等价启动路径),验证可再次到最新 ──
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("re-up from zero: %v", err)
	}
	assertRollbackVersion(t, m, latest)
	for _, table := range []string{"tenants", "users", "platform_settings"} {
		if !rollbackTestHasTable(t, ctx, pool, table) {
			t.Fatalf("table %s missing after re-up", table)
		}
	}
}

// assertRollbackVersion 断言迁移元数据版本与 dirty 状态。
func assertRollbackVersion(t *testing.T, m *migrate.Migrate, want uint) {
	t.Helper()
	v, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("version check: %v", err)
	}
	if dirty || v != want {
		t.Fatalf("version = %d (dirty=%t), want %d", v, dirty, want)
	}
}

// rollbackTestCreateDB 创建独立测试数据库并返回其 URL。
// CREATE/DROP DATABASE 必须在指向另一库(此处 postgres)的连接、事务外执行,
// 不能在被删库的连接上执行,否则 DROP 失败只留残留。
// t.Cleanup 以 LIFO 执行:migrate → pool → DROP(admin 连接仍存活) → admin 关闭。
func rollbackTestCreateDB(t *testing.T, baseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	adminURL := *parsed
	adminURL.Path = "/postgres" // 管理连接指向 postgres 库,不指向被测库
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	// 本地反复运行卫生:先清理历史崩溃残留(本前缀由本测试独占)
	rollbackTestDropStaleDBs(t, ctx, admin)

	dbName := rollbackTestDBPrefix + fmt.Sprintf("%x", time.Now().UnixNano())
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
	return dbURL.String()
}

// rollbackTestDropStaleDBs 删除历史崩溃残留的测试库(仅本前缀,由本测试独占)。
func rollbackTestDropStaleDBs(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1`, rollbackTestDBPrefix+"%")
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

// rollbackTestLatestVersion 从 sql/ 目录推导最新迁移版本,新增迁移自动覆盖。
func rollbackTestLatestVersion(t *testing.T) uint {
	t.Helper()
	entries, err := os.ReadDir("sql")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var latest uint
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		versionStr := strings.SplitN(strings.TrimSuffix(name, ".up.sql"), "_", 2)[0]
		v, err := strconv.ParseUint(versionStr, 10, 64)
		if err != nil {
			t.Fatalf("parse migration version %q: %v", versionStr, err)
		}
		if uint(v) > latest {
			latest = uint(v)
		}
	}
	if latest == 0 {
		t.Fatal("no up migrations found")
	}
	return latest
}

// rollbackTestHasTable 冒烟断言 public schema 中表是否存在
// (to_regclass 全限定,不依赖 search_path)。
func rollbackTestHasTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}
