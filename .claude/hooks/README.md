# Claude / Codex Hooks

确定性护栏，在工具调用生命周期上强制项目红线。**单一真相源**：所有逻辑在
`.claude/hooks/*.sh`，`.codex/hooks.json` 全部转发至此，禁止再出现平行副本。

## 脚本

| 脚本 | 触发 | 职责 |
|------|------|------|
| `lib.sh` | — | 公共库：stdin 解析 + PreToolUse/PostToolUse JSON 输出原语 |
| `guard-bash.sh` | PreToolUse:Bash | 拦截 `rm -rf`/`sudo`/`chmod 777`/`chown`/`dd if=`/`mkfs`/`fdisk` |
| `guard-fs.sh` | PreToolUse:Read\|Write\|Edit | 凭据文件(读写都拦) · 系统目录/prod.yaml(仅拦写) |
| `check-migration-tenant-tables.sh` | PreToolUse:Write\|Edit | 编号迁移禁碰 tenant-only 表(表名从 `tenant_schema.sql` 动态提取) |
| `go-quality.sh` | PostToolUse:Write\|Edit | `.go` 文件 gofmt 写回 + `go vet` 可见反馈 |
| `run-tests.sh` | 手动 / CI | 断言各 guard 的 (输入→decision) 映射 |

## 输出协议（Claude Code）

- PreToolUse 拒绝用 `hookSpecificOutput.permissionDecision:"deny"` + `permissionDecisionReason`
  —— **只否决当前这一次调用**并回喂原因；**不要**用顶层 `{"continue":false}`（那会中止整个 turn）。
- PostToolUse 反馈用 `hookSpecificOutput.additionalContext`（非阻塞注入，成功则静默）。

## 分层决策：编辑轻量 / CI 重量

PostToolUse 只跑 `gofmt`(毫秒级) + 单包 `go vet`(90s 硬顶)——**每次存文件都跑，必须快**。
`golangci-lint`(v2.12) 全量 lint 慢，留给 `make be-lint` 与 CI，**不**进 PostToolUse，避免拖垮编辑体验。

## 约定

- 脚本用 `bash <script>` 调用（不依赖 +x 权限，可移植）。
- 改动任一 guard 后必须 `bash .claude/hooks/run-tests.sh` 全绿再提交。
- 新增 tenant 表只改 `pkg/storage/postgres/tenant_schema.sql`，迁移 guard 自动跟随，无需改脚本。
- 教训：迁移 guard 曾因 schema 路径写错(`pkg/tenantdb/`)静默放行数周——**schema 缺失现在硬失败而非放行**，护栏假在场比没有更危险。
