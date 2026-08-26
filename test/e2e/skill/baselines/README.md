# Baselines（基线占位说明）

本目录存放单点评测的录制基线文件。**首期不提交任何基线文件**——没有基线即首次录制路径：`loadBaseline` 对缺失文件返回 nil，`compareBaseline` 直接判定无回归。

本 point（`onboarding-walkthrough`）的基线文件为 `onboarding-walkthrough.json`（由 `points/onboarding-walkthrough.yaml` 的 `baseline: ../baselines/onboarding-walkthrough.json` 指向）。

首次录制基线（本地验证通过后显式执行）：

```bash
# 智谱系模型：judge 走 OpenAI 兼容 /chat/completions
export LLM_BASE_URL="https://open.bigmodel.cn/api/paas/v4"
export ZHIPU_API_KEY="<key>"
go run ./cmd/e2e-eval-check --kind skill --point onboarding-walkthrough \
  --record-baseline --confirm-record
```

`${LLM_BASE_URL}` 由 CLI 在构造 judge client 时展开，`ZHIPU_API_KEY` 经 `api_key_env` 读取，只进 Authorization header。API key 勿写入任何文件或日志。

录制产生的 `onboarding-walkthrough.json` 是评审产物，需人工 review 后单独提交；禁止隐式更新基线（`--record-baseline` 必须配 `--confirm-record`）。
