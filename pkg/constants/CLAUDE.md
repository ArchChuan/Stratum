# pkg/constants/ — 常量上下文

> 本地薄入口（git 不追踪）。

魔法数字集中管理规则。新增跨包共享常量放本包 `<domain>.go`，命名 `Default*` / `Max*` / `*Timeout` / `*TTL`。**禁止 import `internal/`**（单向依赖）。

@../../docs/agent/constants.md
