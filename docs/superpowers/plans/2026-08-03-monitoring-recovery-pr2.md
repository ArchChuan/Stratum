# Monitoring Recovery PR2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立远端 PostgreSQL 每日备份 + CI 恢复演练（GitHub Actions artifact，保留 30 天），并补充备份/快照恢复 runbook；阿里云云盘快照作为节点/磁盘故障兜底（控制台确认项，仓库侧不写入）。

**Architecture:** 3 个独立单元：备份脚本（kubectl exec pg_dump）、恢复演练脚本（临时 PG + 校验）、定时工作流（backup + restore-drill 两 job）；外加 runbook。备份数据离开单节点，恢复可验证。

**Tech Stack:** Bash、PostgreSQL 16、kubectl、GitHub Actions、Docker（本地验证）。

## 执行环境约定

仓库规定禁止在 main 直接提交；本计划全部命令基于 worktree
`/home/yang/go-projects/stratum-monitoring-recovery`。普通命令用
`cd /home/yang/go-projects/stratum-monitoring-recovery && ...`；git 写命令统一用
`git -C /home/yang/go-projects/stratum-monitoring-recovery ...`。

---

### Task 1: 备份脚本

**Files:**

- Create: `scripts/backup/backup-postgres.sh`
- Test: 本地 `bash -n` 与远端只读演练（Task 5）

- [ ] **Step 1: 创建 `scripts/backup/backup-postgres.sh`**

```bash
#!/usr/bin/env bash

set -euo pipefail

# Dump the stratum PostgreSQL database to a gzip-compressed custom-format file.
# The password is read from the container's own POSTGRES_PASSWORD env var
# (sourced from the stratum-secrets secret); it never appears on the runner.
#
# Usage:
#   BACKUP_NAMESPACE=stratum BACKUP_DEPLOYMENT=stratum-postgresql \
#     BACKUP_OUTPUT=/tmp/backup.sql.gz bash scripts/backup/backup-postgres.sh

NAMESPACE="${BACKUP_NAMESPACE:-stratum}"
DEPLOYMENT="${BACKUP_DEPLOYMENT:-stratum-postgresql}"
OUTPUT="${BACKUP_OUTPUT:-backup.sql.gz}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "backup-postgres: required command unavailable: $1" >&2
    exit 1
  }
}
for command_name in kubectl gzip sha256sum; do
  require_command "${command_name}"
done

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

kubectl exec "deploy/${DEPLOYMENT}" -n "${NAMESPACE}" -- \
  sh -c 'PGPASSWORD="${POSTGRES_PASSWORD}" pg_dump -Fc -U stratum -d stratum' \
  >"${tmp_dir}/dump.pg"
gzip -c "${tmp_dir}/dump.pg" >"${OUTPUT}"
sha256sum "${OUTPUT}" >"${OUTPUT}.sha256"
echo "backup complete: ${OUTPUT} ($(du -h "${OUTPUT}" | cut -f1))"
```

- [ ] **Step 2: 语法检查**

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && bash -n scripts/backup/backup-postgres.sh`
Expected: 无输出（语法通过）。若本机有 `shellcheck`，运行 `shellcheck scripts/backup/backup-postgres.sh` 并修复告警。

- [ ] **Step 3: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add scripts/backup/backup-postgres.sh
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[feat](backup): add postgres dump script"
```

---

### Task 2: 恢复演练脚本

**Files:**

- Create: `scripts/backup/restore-drill.sh`
- Test: 本地 docker PostgreSQL 演练（Task 5）

- [ ] **Step 1: 创建 `scripts/backup/restore-drill.sh`**

```bash
#!/usr/bin/env bash

set -euo pipefail

# Restore a gzip-compressed custom-format pg_dump into a scratch database and
# validate that public tables, tenant schemas, and key rows survive. Fails
# closed when any check fails.
#
# Usage:
#   PGHOST=127.0.0.1 PGPORT=5432 PGUSER=postgres PGPASSWORD=postgres \
#     bash scripts/backup/restore-drill.sh <dump.sql.gz> [report.txt]

DUMP_GZ="${1:?usage: restore-drill.sh <dump.sql.gz> [report.txt]}"
REPORT="${2:-backup-drill-report.txt}"
: "${PGHOST:=127.0.0.1}"
: "${PGPORT:=5432}"
: "${PGUSER:=postgres}"
: "${PGPASSWORD:=postgres}"
: "${PGDATABASE:=stratum_drill}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "restore-drill: required command unavailable: $1" >&2
    exit 1
  }
}
for command_name in psql pg_restore gunzip; do
  require_command "${command_name}"
done

export PGPASSWORD
: >"${REPORT}"

fail() {
  echo "restore-drill: $*" >&2
  echo "FAIL: $*" >>"${REPORT}"
  exit 1
}

psql -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS ${PGDATABASE};" \
  -c "CREATE DATABASE ${PGDATABASE};" >/dev/null \
  || fail "cannot create drill database"

gunzip -c "${DUMP_GZ}" \
  | pg_restore -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" \
      -d "${PGDATABASE}" --no-owner --no-privileges \
  || fail "pg_restore failed"

run_query() {
  psql -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" \
    -d "${PGDATABASE}" -tAc "$1"
}

tenants="$(run_query 'SELECT count(*) FROM public.tenants;')"
users="$(run_query 'SELECT count(*) FROM public.users;')"
tenant_schemas="$(run_query "SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'tenant\\_%';")"

[[ "${tenants}" =~ ^[0-9]+$ && "${tenants}" -gt 0 ]] \
  || fail "public.tenants rows: ${tenants:-missing}"
[[ "${users}" =~ ^[0-9]+$ && "${users}" -gt 0 ]] \
  || fail "public.users rows: ${users:-missing}"
[[ "${tenant_schemas}" =~ ^[0-9]+$ && "${tenant_schemas}" -gt 0 ]] \
  || fail "tenant schemas: ${tenant_schemas:-missing}"

while IFS= read -r schema_name; do
  [[ -n "${schema_name}" ]] || continue
  table_count="$(run_query "SELECT count(*) FROM information_schema.tables WHERE table_schema = '${schema_name}';")"
  [[ "${table_count}" =~ ^[0-9]+$ && "${table_count}" -gt 0 ]] \
    || fail "tenant schema ${schema_name} has no tables"
done < <(run_query "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'tenant\\_%' ORDER BY 1;")

{
  echo "PASS"
  echo "public.tenants=${tenants}"
  echo "public.users=${users}"
  echo "tenant_schemas=${tenant_schemas}"
  echo "restored_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >>"${REPORT}"
echo "restore drill passed"
cat "${REPORT}"
```

- [ ] **Step 2: 语法检查**

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && bash -n scripts/backup/restore-drill.sh`
Expected: 无输出（语法通过）。若本机有 `shellcheck`，运行并修复告警。

- [ ] **Step 3: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add scripts/backup/restore-drill.sh
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[feat](backup): add restore drill script"
```

---

### Task 3: 备份工作流

**Files:**

- Create: `.github/workflows/backup.yml`

- [ ] **Step 1: 创建 `.github/workflows/backup.yml`**

```yaml
name: Postgres backup and restore drill

on:
  schedule:
    - cron: '0 18 * * *'
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: stratum-postgres-backup
  cancel-in-progress: false

jobs:
  backup:
    name: Dump PostgreSQL
    runs-on: ubuntu-latest
    environment: production
    steps:
      - uses: actions/checkout@v4

      - uses: azure/setup-kubectl@v4
        with:
          version: 'v1.28.0'

      - name: Setup SSH tunnel to K3s
        run: |
          mkdir -p ~/.ssh
          echo "${{ secrets.SSH_DEPLOY_KEY }}" > ~/.ssh/deploy_key
          echo "${{ secrets.SSH_KNOWN_HOSTS }}" > ~/.ssh/known_hosts
          chmod 600 ~/.ssh/deploy_key
          chmod 600 ~/.ssh/known_hosts
          ssh -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.ssh/known_hosts -o ExitOnForwardFailure=yes \
            -i ~/.ssh/deploy_key \
            -fN -L 6443:127.0.0.1:6443 \
            root@${{ secrets.SSH_DEPLOY_HOST }}
          sleep 2

      - name: Configure kubeconfig
        run: |
          mkdir -p $HOME/.kube
          echo "${{ secrets.KUBE_CONFIG }}" | base64 -d > $HOME/.kube/config
          chmod 600 $HOME/.kube/config

      - name: Verify cluster
        run: kubectl cluster-info

      - name: Dump PostgreSQL
        run: |
          mkdir -p backup-artifacts
          BACKUP_OUTPUT="backup-artifacts/backup.sql.gz" bash scripts/backup/backup-postgres.sh

      - name: Attach backup metadata
        run: |
          date -u +%Y-%m-%dT%H:%M:%SZ > backup-artifacts/timestamp.txt
          cat backup-artifacts/backup.sql.gz.sha256

      - name: Upload backup artifact
        uses: actions/upload-artifact@v4
        with:
          name: stratum-postgres-backup
          path: backup-artifacts/
          retention-days: 30

  restore-drill:
    name: Restore drill
    runs-on: ubuntu-latest
    needs: backup
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: postgres
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    steps:
      - uses: actions/checkout@v4

      - uses: actions/download-artifact@v4
        with:
          name: stratum-postgres-backup
          path: backup-artifacts/

      - name: Run restore drill
        env:
          PGHOST: 127.0.0.1
          PGPORT: '5432'
          PGUSER: postgres
          PGPASSWORD: postgres
        run: |
          bash scripts/backup/restore-drill.sh backup-artifacts/backup.sql.gz backup-drill-report.txt

      - name: Upload drill report
        uses: actions/upload-artifact@v4
        with:
          name: stratum-backup-drill-report
          path: backup-drill-report.txt
          retention-days: 30
```

- [ ] **Step 2: YAML 校验**

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && ruby -e "require 'yaml'; YAML.load_file('.github/workflows/backup.yml'); puts 'yaml ok'"`（若本机有 ruby），否则用仓库 pre-commit 的 check-yaml 钩子在提交时校验。
Expected: `yaml ok` 或提交钩子通过。

- [ ] **Step 3: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add .github/workflows/backup.yml
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[feat](backup): add daily postgres backup and restore drill workflow"
```

---

### Task 4: 备份与快照恢复 runbook

**Files:**

- Create: `docs/operations/backup-restore-runbook.md`

- [ ] **Step 1: 创建 `docs/operations/backup-restore-runbook.md`**

```markdown
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

1. 在隔离环境启动 PostgreSQL 16 并恢复：

   ```bash
   docker run -d --name stratum-restore -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=postgres -p 5433:5432 postgres:16
   gunzip -c backup.sql.gz | \
     docker exec -i stratum-restore pg_restore -U postgres -d postgres \
       --no-owner --no-privileges
   ```

2. 校验 public 表、租户 schema 与关键表行数（复用恢复演练脚本）：

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

```

- [ ] **Step 2: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add docs/operations/backup-restore-runbook.md
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[docs](backup): add backup and snapshot restore runbook"
```

---

### Task 5: 本地验证并收尾

- [ ] **Step 1: 本地恢复演练**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
docker run -d --name stratum-drill-src -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=stratum postgres:16
until docker exec stratum-drill-src pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
docker exec stratum-drill-src psql -U postgres -d stratum -v ON_ERROR_STOP=1 \
  -c "CREATE TABLE public.tenants(id int); INSERT INTO public.tenants VALUES (1);" \
  -c "CREATE TABLE public.users(id int); INSERT INTO public.users VALUES (1);" \
  -c "CREATE SCHEMA tenant_demo; CREATE TABLE tenant_demo.agents(id int);" >/dev/null
docker exec stratum-drill-src sh -c 'pg_dump -Fc -U postgres -d stratum | gzip' > /tmp/stratum-drill.sql.gz
docker run -d --name stratum-drill-dst -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgres -p 5433:5432 postgres:16
until docker exec stratum-drill-dst pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
PGHOST=127.0.0.1 PGPORT=5433 PGUSER=postgres PGPASSWORD=postgres \
  bash scripts/backup/restore-drill.sh /tmp/stratum-drill.sql.gz /tmp/backup-drill-report.txt
cat /tmp/backup-drill-report.txt
docker rm -f stratum-drill-src stratum-drill-dst >/dev/null
```

Expected: 报告首行为 `PASS`，`public.tenants=1`、`public.users=1`、`tenant_schemas=1`。

- [ ] **Step 2: 失败路径验证（可选但推荐）**

构造一个不含 `tenant_%` schema 的 dump，确认 `restore-drill.sh` 非零退出且报告含 FAIL：

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
docker run -d --name stratum-drill-bad-src -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=stratum postgres:16
until docker exec stratum-drill-bad-src pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
docker exec stratum-drill-bad-src psql -U postgres -d stratum -v ON_ERROR_STOP=1 \
  -c "CREATE TABLE public.tenants(id int); INSERT INTO public.tenants VALUES (1);" \
  -c "CREATE TABLE public.users(id int); INSERT INTO public.users VALUES (1);" >/dev/null
docker exec stratum-drill-bad-src sh -c 'pg_dump -Fc -U postgres -d stratum | gzip' > /tmp/stratum-drill-bad.sql.gz
docker run -d --name stratum-drill-bad-dst -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgres -p 5434:5432 postgres:16
until docker exec stratum-drill-bad-dst pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
set +e
PGHOST=127.0.0.1 PGPORT=5434 PGUSER=postgres PGPASSWORD=postgres \
  bash scripts/backup/restore-drill.sh /tmp/stratum-drill-bad.sql.gz /tmp/backup-drill-bad-report.txt
status=$?
set -e
docker rm -f stratum-drill-bad-src stratum-drill-bad-dst >/dev/null
[[ "$status" -ne 0 ]] && grep -q '^FAIL' /tmp/backup-drill-bad-report.txt && echo "fail-closed verified"
```

Expected: 输出 `fail-closed verified`。

- [ ] **Step 3: 全量守卫（PR2 不涉及 Go/监控规则，仅跑轻量项）**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
bash -n scripts/backup/*.sh
git -C /home/yang/go-projects/stratum-monitoring-recovery diff --check origin/main..HEAD
```

Expected: 无输出。若 `make risk-guardrails` 对工作流/脚本命中部署类检查，运行一次确认通过。

- [ ] **Step 4: 确认改动清单**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery log --oneline origin/main..HEAD
git -C /home/yang/go-projects/stratum-monitoring-recovery diff --stat origin/main..HEAD
```

Expected: PR2 提交（脚本 x2、workflow、runbook），不含 PR1 文件。

- [ ] **Step 5: 推送并创建 PR（需用户确认后执行）**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery push -u origin feat/monitoring-recovery
gh pr create --base main \
  --title "[feat](backup): daily postgres backup with restore drill" \
  --body "What: scripts/backup/backup-postgres.sh（kubectl exec pg_dump -Fc，密码仅在容器内）；scripts/backup/restore-drill.sh（临时 PG 恢复并校验 public 表/租户 schema/关键表行数，fail closed）；.github/workflows/backup.yml 每日备份 + 恢复演练 + artifact 保留 30 天；备份/快照恢复 runbook。\nWhy: 远端 PostgreSQL 无自动化备份且无恢复演练；阿里云云盘快照仅覆盖整机级故障，无法验证可恢复性。\nHowToTest: bash -n scripts/backup/*.sh；本地 docker 演练（PASS 与 fail-closed 两种）；合并后手动触发一次 backup.yml 验证 restore-drill 通过。"
```

PR 描述包含 What/Why/HowToTest；首次备份工作流运行与阿里云快照策略确认在合并后执行。
