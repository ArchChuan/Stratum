package factcheck

import (
	"context"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// Verdict 固定枚举，judge 输出与 Report 推导共用。
const (
	VerdictSupported    = "SUPPORTED"
	VerdictContradicted = "CONTRADICTED"
	VerdictUnsupported  = "UNSUPPORTED"
)

// Judge 是 LLM-as-Judge 的最小接口（消费方 port）：对一批 claim + 聚合证据，
// 返回逐条判定。由组合根用 llmgateway completer 实现，factcheck 不 import
// llmgateway domain（DDD：跨 context 接口定义在消费方）。
type Judge interface {
	JudgeClaims(ctx context.Context, claims []string, evidence string) ([]domain.ClaimVerdict, error)
}

// Settings 是幻觉校验的执行参数与依赖。Enabled 默认 false（fail-closed）；
// EvidenceFn 是 per-execution 的证据检索 fn（执行时由
// ExecutionConfig.RAGSearchFnWithEvidence 注入，已带 tenant 权限上下文）。
type Settings struct {
	Enabled    bool
	Judge      Judge
	EvidenceFn func(ctx context.Context, workspaces []string, query string, topK int, viewerID string) (port.RAGSearchEvidence, error)
	TopK       int
	MaxClaims  int
	// CitationVerify 开启对账轨（代码级核验最终输出中的 <tool_ref:ID> 声称 vs
	// ToolObservation 记录），可与 Enabled 独立：Enabled=false 时单开也能跑对账，
	// 此时 Judge/EvidenceFn 可缺失（对账轨不依赖 judge）。
	CitationVerify bool
	// Logger 记录降级原因（不记原始输出/PII）；nil 时静默。
	Logger *zap.Logger
}

// Checker 校验一段输出的 claim 是否有 RAG 证据支撑。advisory 定位：返回展示型
// 报告，只透出前端，不进工具决策、不写库为 ground truth。
type Checker interface {
	Check(ctx context.Context, input domain.FactCheckInput) *domain.FactCheckReport
}

type checker struct {
	settings Settings
}

// New 构造 Checker。TopK/MaxClaims 为 0 时回退常量默认。
func New(s Settings) Checker {
	if s.TopK <= 0 {
		s.TopK = constants.AgentFactCheckTopK
	}
	if s.MaxClaims <= 0 {
		s.MaxClaims = constants.AgentFactCheckMaxClaims
	}
	return &checker{settings: s}
}

func (c *checker) Check(ctx context.Context, input domain.FactCheckInput) *domain.FactCheckReport {
	// fail-closed：开关关 / 依赖缺失 / 空 viewerID 均不校验。viewerID 空时
	// RAGService 的 SkipAccessCheck 会整体旁路 D2 门控，必须在此显式守卫，
	// 不依赖检索 fn 内部的条件。对账轨（CitationVerify + 有观察记录）不依赖
	// judge/viewerID，可与 judge 轨独立开启。
	judgeOn := c.settings.Enabled && c.settings.Judge != nil && c.settings.EvidenceFn != nil && input.ViewerID != ""
	citationOn := c.settings.CitationVerify && len(input.ToolObservations) > 0
	if !judgeOn && !citationOn {
		c.warn("factcheck skip: fail-closed",
			zap.Bool("enabled", c.settings.Enabled),
			zap.Bool("citation_verify", c.settings.CitationVerify),
			zap.Bool("judge_nil", c.settings.Judge == nil),
			zap.Bool("evidence_nil", c.settings.EvidenceFn == nil),
			zap.Bool("viewer_empty", input.ViewerID == ""),
			zap.Int("observations", len(input.ToolObservations)))
		return nil
	}
	report := &domain.FactCheckReport{Checked: true, IsValid: true}
	if citationOn {
		c.reconcileReport(input, report)
	}
	if !judgeOn {
		return report
	}
	return c.judgeReport(ctx, input, report, citationOn)
}

// reconcileReport 跑对账轨：最终输出中的引用 vs 内存 ToolObservation 记录，
// 叠加 ToolReferences/Unverified 并推导 IsValid 与 RiskPoints。对账是代码级
// 核验，judge 轨不可用时仍是独立事实核验。
func (c *checker) reconcileReport(input domain.FactCheckInput, report *domain.FactCheckReport) {
	refs, unverified := reconcileCitations(input.Output, input.ToolObservations)
	report.ToolReferences = refs
	report.UnverifiedClaims = unverified
	report.UnverifiedCount = len(unverified)
	for _, ref := range refs {
		if ref.Classification == ClassVerificationFailed || ref.Classification == ClassInvalidReference {
			report.IsValid = false
			report.RiskPoints++
		}
	}
}

// judgeReport 跑 LLM-as-Judge 轨：拆 claim → 检索证据 → 判定 → 叠加到 report。
// judge 依赖失败时：有对账结果则保留（对账不被 judge 失败吞掉），否则沿既有
// fail-open 返回 nil（不阻塞 agent 执行）。
func (c *checker) judgeReport(ctx context.Context, input domain.FactCheckInput, report *domain.FactCheckReport, citationOn bool) *domain.FactCheckReport {
	claims := extractClaims(input.Output, c.settings.MaxClaims)
	if len(claims) == 0 {
		return report
	}
	// 独立超时预算：检索/judge 慢或超时不阻塞 agent 执行，直接降级。
	ctx, cancel := context.WithTimeout(ctx, constants.AgentFactCheckTimeout)
	defer cancel()

	evidence, err := c.gatherEvidence(ctx, input, claims)
	if err != nil {
		c.warn("factcheck skip: evidence search failed", zap.Int("claims", len(claims)), zap.Error(err))
		return degradeReport(report, citationOn)
	}
	if evidence == "" {
		c.warn("factcheck skip: no evidence", zap.Int("claims", len(claims)))
		return degradeReport(report, citationOn)
	}
	c.warn("factcheck evidence ok", zap.Int("claims", len(claims)), zap.Int("evidence_bytes", len(evidence)))
	verdicts, err := c.settings.Judge.JudgeClaims(ctx, claims, evidence)
	if err != nil {
		c.warn("factcheck skip: judge failed", zap.Int("claims", len(claims)), zap.Error(err))
		return degradeReport(report, citationOn)
	}
	c.warn("factcheck verdicts ok", zap.Int("verdicts", len(verdicts)))
	judgeResult := summarize(verdicts)
	report.Claims = judgeResult.Claims
	if !judgeResult.IsValid {
		report.IsValid = false
	}
	report.RiskPoints += judgeResult.RiskPoints
	return report
}

// degradeReport 对 judge 轨降级：无对账结果时返回 nil（沿既有 fail-open），
// 有对账结果时保留 report（对账是独立事实核验，不应被 judge 失败吞掉）。
func degradeReport(report *domain.FactCheckReport, citationOn bool) *domain.FactCheckReport {
	if !citationOn {
		return nil
	}
	return report
}

func (c *checker) warn(msg string, fields ...zap.Field) {
	if c.settings.Logger != nil {
		c.settings.Logger.Warn(msg, fields...)
	}
}

// gatherEvidence 逐 claim 独立 RAG 检索并拼接证据（每 claim 一行 + 其命中内容），
// 使 judge 能区分各 claim 的证据支撑。检索并行执行（有界并发，信号量限流）但
// 结果按 claim 索引保序写入，保证证据拼接顺序与 claims 输入一致。任一检索失败
// 即传播首个错误（errgroup 取消其余），沿既有降级路径返回。
func (c *checker) gatherEvidence(ctx context.Context, input domain.FactCheckInput, claims []string) (string, error) {
	parts := make([]string, len(claims))
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, constants.AgentFactCheckEvidenceConcurrency)
	for i, claim := range claims {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			ev, err := c.settings.EvidenceFn(gctx, input.Workspaces, claim, c.settings.TopK, input.ViewerID)
			if err != nil {
				return err
			}
			if strings.TrimSpace(ev.Content) == "" {
				return nil
			}
			parts[i] = fmt.Sprintf("[claim %d] %s\n%s", i, claim, ev.Content)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n"), nil
}

// extractClaims 从最终输出提取待校验的候选断言：先整体剥掉 markdown code
// fence 块，再 SplitSentences 切分、剔除单行工具调用。模型常把工具调用写成
// 代码块文本而非真正执行；这些噪声既非事实断言，又会挤占 MaxClaims 配额，
// 把后续真实断言（如"平台基于 Kafka"）截掉。元话语（"让我…"/"我需要…"）由
// judge 判 UNSUPPORTED，不在此过滤（避免过度设计）。
func extractClaims(output string, max int) []string {
	output = stripCodeFences(output)
	sentences := textchunk.SplitSentences(output)
	claims := make([]string, 0, len(sentences))
	for _, s := range sentences {
		if isNonFactual(s) {
			continue
		}
		claims = append(claims, s)
	}
	return trimClaims(claims, max)
}

// stripCodeFences 删除 ``` 包裹的多行代码块（含 fence 标记行本身）。逐行处理
// 而非先切句，避免块内多行 JSON 被 \n 拆成零碎单行 claim。
func stripCodeFences(s string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// isNonFactual 判定一段文本是否非事实断言：单行工具调用（JSON tool_code 块
// 或 foo(args)）。多行 code fence 由 stripCodeFences 先行剥除，此处兜底
// 单行形式。
func isNonFactual(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") && strings.Contains(t, "tool_code") {
		return true
	}
	if before, _, ok := strings.Cut(t, "("); ok && strings.HasSuffix(t, ")") {
		return strings.TrimSpace(before) != "" && !strings.Contains(before, " ") && !strings.Contains(before, "（")
	}
	return false
}

// trimClaims 去空 claim 并截前 MaxClaims（控成本）。
func trimClaims(claims []string, max int) []string {
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		if s := strings.TrimSpace(claim); s != "" {
			out = append(out, s)
		}
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// summarize 由 verdict 推导整体 IsValid 与 RiskPoints：任一 claim 被判
// CONTRADICTED/UNSUPPORTED 即整体无效。
func summarize(verdicts []domain.ClaimVerdict) *domain.FactCheckReport {
	report := &domain.FactCheckReport{Checked: true, Claims: verdicts, IsValid: true}
	for _, v := range verdicts {
		if v.Verdict == VerdictContradicted || v.Verdict == VerdictUnsupported {
			report.IsValid = false
			report.RiskPoints++
		}
	}
	return report
}
