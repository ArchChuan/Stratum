# Verification Manifest

`.test/verification.yaml` 是风险、检查、capability、审查和 attestation 最低要求的单一事实源。

- Agent 可以建议提高风险等级，不能降低确定性分类结果。
- 缺失或无法解析 manifest 时 fail closed。
- `skipped` 和 `unreconciled` 必须为零才能进入 `accepted`。
- manifest digest 必须同时进入 verification plan、运行 attestation 和 completion report。
- planner 必须解析 manifest 的规则、mode、checks 和 reviews；调用方只能设置 minimum risk 或显式 release intent。
- R3 不得把 evidence JSON 哈希冒充发布 artifact；R4 必须提供经过签名证明的真实构建或 OCI digest。
- canonical 命令只有在真实实现存在时才允许暴露；禁止以 manifest 检查冒充 plan、CI 或 attestation 验证。

R0-R4 的检查集合是最低要求。项目可以追加检查，但不得在调用方静默移除 manifest 要求。
