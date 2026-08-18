package port

import "context"

// SufficiencyVerdict 是证据充分性 judge 的固定枚举：evidence 能否支撑
// query 的结论（与相似度阈值正交——像不像 vs 能不能推出）。
type SufficiencyVerdict string

const (
	SufficiencySufficient   SufficiencyVerdict = "SUFFICIENT"
	SufficiencyInsufficient SufficiencyVerdict = "INSUFFICIENT"
)

// SufficiencyJudge 是生成前证据充分性门（消费方 port）：对 query + 聚合
// 证据判定证据是否充分。由组合根用 llmgateway completer 实现，knowledge
// 永不 import llmgateway（DDD：跨 context 接口定义在消费方，照抄
// agent/factcheck.Judge 模式）。实现方必须 fail-closed：任何错误向上
// 返回，由应用层降级为"未判定"，绝不默认放行。
type SufficiencyJudge interface {
	JudgeSufficiency(ctx context.Context, query, evidence string) (SufficiencyVerdict, error)
}

// FaithfulnessVerdict 是生成后 claim 级支撑判定（P3 评估层消费）。
type FaithfulnessVerdict string

const (
	FaithfulnessSupported   FaithfulnessVerdict = "SUPPORTED"
	FaithfulnessUnsupported FaithfulnessVerdict = "UNSUPPORTED"
)

// FaithfulnessJudge 判定答案的逐 claim 是否被证据支撑（评估指标消费方
// port，不进工具决策，advisory 定位同 agent factcheck）。
type FaithfulnessJudge interface {
	JudgeFaithfulness(ctx context.Context, answer, evidence string) ([]FaithfulnessVerdict, error)
}

// ContradictionJudge 检测证据内部矛盾（文档级：≥2 不同文档立场冲突）。
type ContradictionJudge interface {
	JudgeContradiction(ctx context.Context, query, evidence string) (bool, error)
}
