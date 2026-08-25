# Baselines（基线占位说明）

本目录存放单点评测的录制基线文件。**首期不提交任何基线文件**——没有基线即首次录制路径：`loadBaseline` 对缺失文件返回 nil，`compareBaseline` 直接判定无回归。

本 point（`planner-agent`）的基线文件为 `planner-agent.json`（由 `points/planner-agent.yaml` 的 `baseline: ../baselines/planner-agent.json` 指向）。

首次录制基线（本地验证通过后显式执行）：

```bash
go run ./cmd/e2e-eval-check --kind agent --point planner-agent \
  --record-baseline --confirm-record
```

录制产生的 `planner-agent.json` 是评审产物，需人工 review 后单独提交；禁止隐式更新基线（`--record-baseline` 必须配 `--confirm-record`）。
