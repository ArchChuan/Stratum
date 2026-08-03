# 备份与恢复运行手册

## 概览

远端 PostgreSQL 由 `.github/workflows/backup.yml` 每日（北京时间 02:00）执行
`scripts/backup/backup-postgres.sh` 导出 custom-format dump，上传 GitHub Actions
artifact（`stratum-postgres-backup`，保留 30 天），并由 `restore-drill` job 在临时
PostgreSQL 16 中执行 `scripts/backup/restore-drill.sh` 验证可恢复性；校验失败即工作流失败。

阿里云云盘快照作为节点/磁盘/整机故障的基础设施级兜底，与 pg_dump 互补：
快照覆盖整机恢复，pg_dump 覆盖逻辑损坏、误删、选择性恢复与可验证性。

## 恢复 pg_dump artifact

1. 在 GitHub Actions 中打开最近成功的 `Postgres backup and restore drill` run，
   下载 `stratum-postgres-backup` artifact（含 `backup.sql.gz` 与 `backup.sql.gz.sha256`）。
2. 校验完整性：

   ```bash
   sha256sum -c backup.sql.gz.sha256
   ```

3. 在隔离环境启动 PostgreSQL 16 并恢复：

   ```bash
   docker run -d --name stratum-restore -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=postgres -p 5433:5432 postgres:16
   gunzip -c backup.sql.gz | \
     docker exec -i stratum-restore pg_restore -U postgres -d postgres \
       --no-owner --no-privileges
   ```

4. 校验 public 表、租户 schema 与关键表行数（复用恢复演练脚本）：

   ```bash
   PGHOST=127.0.0.1 PGPORT=5433 PGUSER=postgres PGPASSWORD=postgres \
     bash scripts/backup/restore-drill.sh backup.sql.gz
   ```

   恢复演练失败即视为备份不可信，禁止直接用于线上恢复。

## 阿里云快照兜底

- 当前实例磁盘为 `/dev/vda`（阿里云虚拟云盘），适用云盘快照与自动快照策略。
- **待确认项（用户侧）**：在阿里云控制台确认该实例已配置自动快照策略（周期与保留天数），
  并确认快照可正常创建；仓库侧不执行任何写入。
- 整机恢复：从最近快照创建新实例/回滚磁盘后，按 `docs/k8s-deployment.md` 启动 k3s 与
  stratum release，再核对全部工作负载与 `/api/health`。
- 快照恢复是整机级、崩溃一致性：不验证逻辑一致性，也不能选择性恢复单表；遇到逻辑损坏仍
  使用 pg_dump 恢复。

## 安全与保留

- 备份不记录数据库密码；密码只在 PG 容器环境变量中存在，runner 不落盘。
- artifact 保留 30 天；需要长期归档时升级为阿里云 OSS 跨地域存储（后续）。
- 恢复演练在隔离环境执行，禁止覆盖在线 PVC 或在线数据库。
