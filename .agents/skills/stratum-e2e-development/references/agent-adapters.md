# Claude Code and Codex Adapters

Claude Code 与 Codex 只做薄适配：

1. 加载 `stratum-e2e-development` 和 `.test/verification.yaml`。
2. 传入用户目标、diff 范围和当前 commit。
3. 在 clean commit 上调用 `make test-verify-before-pr`，校验 local report freshness。
4. 创建 PR 后等待非浏览器 CI；合并后按需等待 release pipeline 的独立 release record。

`CLAUDE.md`、`AGENTS.md` 和 hooks 只负责触发与机械阻断，不复制风险矩阵或测试语义。Agent 的最终消息不是
attestation。local report 不是 GitHub status，CI result 不是 deployment receipt，release record 也不能证明本地
浏览器执行。任一 authority 缺失时必须准确报告其状态和缺失证据，不得用 `CI=true` 或 review 文本伪造结果。
