# Claude Code and Codex Adapters

Claude Code 与 Codex 只做薄适配：

1. 加载 `stratum-e2e-development` 和 `.test/verification.yaml`。
2. 传入用户目标、diff 范围和当前 commit。
3. 调用仓库真实存在的 canonical 验证入口。
4. 等待受保护 CI，并校验结构化 completion report。

`CLAUDE.md`、`AGENTS.md` 和 hooks 只负责触发与机械阻断，不复制风险矩阵或测试语义。Agent 的最终消息不是
attestation。未得到 `accepted` 报告时必须使用 `failed`、`blocked` 或 `incomplete`，并列出缺失证据。
