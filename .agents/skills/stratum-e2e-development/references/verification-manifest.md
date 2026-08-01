# Verification Manifest

`.test/verification.yaml` 是风险、local checks、CI checks、capability 和 attestation 最低要求的单一事实源。

- `browser_e2e_authority: local`：浏览器只在 PR 前由本地 canonical entrypoint 执行。
- `merge_authority: ci`：GitHub Actions 只用非浏览器检查决定是否合并。
- `deployment_authority: release_pipeline`：部署验证精确 commit、不可变 digest 和运行健康。
- 一个 check ID 只能有一个执行 owner；CI owner 不得包含 browser、Playwright、Chromium 或 stateful browser check。
- Agent 可以建议提高风险等级，不能降低确定性分类结果；缺失或无法解析 manifest 时 fail closed。
- `skipped`、`blocked`、`failed` 和 `unreconciled` 必须为零，cleanup 必须完整，local report 才能为 `passed`。
- manifest digest 必须进入 verification plan、运行 attestation 和 local report；任一变化使报告 `stale`。
- planner 解析 rules、mode、`local_checks` 和 `ci_checks`；调用方只能设置 minimum risk 或显式 release intent。
- canonical 命令只有在真实实现存在时才允许暴露，禁止以字符串检查冒充执行证据。

R0-R4 的集合是最低要求。调用方不得静默删除检查，也不得把本地报告包装成 GitHub status。
