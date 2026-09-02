package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Sentinel errors raised by Workspace domain methods. Application-level
// names mirror these so the HTTP error mapping table can route both layers.
var (
	ErrInvalidEmbeddingModel      = errors.New("unsupported embedding model")
	ErrEmbeddingModelRequired     = errors.New("embedding model is required")
	ErrInvalidQueryMode           = errors.New("invalid query_mode")
	ErrInvalidChunkingStrategy    = errors.New("invalid chunking_strategy")
	ErrInvalidRerankIdentity      = errors.New("invalid reranking identity")
	ErrInvalidScoreThreshold      = errors.New("score_threshold must be within [0, 1]")
	ErrEmbeddingModelImmutable    = errors.New("embedding_model is immutable after creation")
	ErrChunkSizeImmutable         = errors.New("chunk_size is immutable after creation")
	ErrChunkOverlapImmutable      = errors.New("chunk_overlap is immutable after creation")
	ErrChunkingStrategyImmutable  = errors.New("chunking_strategy is immutable after creation")
	ErrRerankModelRequired        = errors.New("rerank model is required")
	ErrInvalidRerankModel         = errors.New("unsupported rerank model")
	ErrInvalidJudgeModel          = errors.New("unsupported judge model")
	ErrInvalidTopK                = errors.New("top_k must be within [1, max]")
	ErrInvalidRerankTopK          = errors.New("rerank_top_k must be within [0, max]")
	ErrInvalidScoringInstructions = errors.New("scoring instructions exceed max runes")
)

const (
	DefaultQueryMode        = "hybrid"
	DefaultChunkSize        = 512
	DefaultChunkOverlap     = 64
	DefaultTopK             = 5
	DefaultChunkingStrategy = ChunkingStrategyStructureRecursive

	// DefaultRerankScoringInstructions / DefaultJudgeScoringInstructions 是新建
	// workspace 时预填的评分指令推荐文本（可直接编辑）。语义与内置评分 prompt
	// 一致，编辑/清空都安全：清空后指令为空，运行时回落代码内置 prompt。
	DefaultRerankScoringInstructions = "按查询与候选片段的语义相关性打分（0-1）：越相关分越高，分数须有明显区分度，避免全部同分或全部高分。结合查询中的限定条件（时间、范围、实体、数量）修正判断。"
	DefaultJudgeScoringInstructions  = "逐条核对证据能否支撑问题所问的结论。证据缺失、仅支持部分结论、或与问题范围不符时判定为证据不足；证据不足时不得猜测作答。"

	ChunkingStrategyRecursive          = "recursive"
	ChunkingStrategyStructureRecursive = "structure_recursive"
	ChunkingStrategySemantic           = "semantic"
)

var AllowedChunkingStrategies = map[string]bool{
	ChunkingStrategyRecursive:          true,
	ChunkingStrategyStructureRecursive: true,
	ChunkingStrategySemantic:           true,
}

// AllowedQueryModes enumerates RAG query strategies recognised by RAGService.
var AllowedQueryModes = map[string]bool{
	"vector": true,
	"graph":  true,
	"hybrid": true,
}

// AllowedRerankIdentities enumerates the local (internal) rerank strategies
// recognised by RAGService: "" (none) and the builtin scorer. External
// providers (e.g. cohere) are no longer whitelisted here — their existence is
// validated against the global catalogue by the application layer
// (port.ModelExists), keeping this domain check free of I/O.
var AllowedRerankIdentities = map[string]bool{
	"":                 true,
	"builtin-score-v1": true,
}

// SplitRerankIdentity splits "provider:model" style rerank identities;
// bare strings are provider-only (e.g. builtin-score-v1).
func SplitRerankIdentity(identity string) (provider, model string) {
	if i := strings.Index(identity, ":"); i >= 0 {
		return identity[:i], identity[i+1:]
	}
	return identity, ""
}

// Workspace is a knowledge RAG workspace owned by a tenant.
type Workspace struct {
	ID          string
	Name        string
	Description string
	Config      WorkspaceConfig
	// CreatedBy is the user who created the workspace ("" for historical/platform rows).
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WorkspaceConfig is the per-workspace RAG configuration persisted as JSONB.
type WorkspaceConfig struct {
	EmbeddingModel   string
	ChunkSize        int
	ChunkOverlap     int
	QueryMode        string
	TopK             int
	ChunkingStrategy string
	// Reranking is the rerank strategy identity: "" (none),
	// "builtin-score-v1" (local score desc), or "provider:model" for an
	// external reranker. ScoreThreshold keeps only results with sim >=
	// threshold (0 disables); RerankTopK is the final count after external
	// reranking (0 uses TopK).
	Reranking      string
	ScoreThreshold float32
	RerankTopK     int
	// RerankModel 是 builtin-score-v1 的 LLM 语义重排模型（workspace 显式配置）；
	// 空 = builtin 未装配（保存被拒，见 Validate）。JudgeModel 是证据充分性 judge
	// 模型；空 = judge 门关闭（fail-closed 放行）。
	RerankModel string
	JudgeModel  string
	// RerankScoringInstructions 是内置重排（builtin-score-v1）的评分指令附加段；
	// JudgeScoringInstructions 是证据充分性 judge 的评分指令附加段。空 = 使用
	// 代码内置评分 prompt；JSON 输出结构固定不可编辑（解析安全依赖）。
	RerankScoringInstructions string
	JudgeScoringInstructions  string
}

// ScoreThresholdResetSentinel 是 MergeUpdate 对「显式 0」的编码：partial 合并
// 以零值表示"未提供"，但 score_threshold=0 是合法值（关闭过滤）。handler
// PATCH（整体替换契约）把用户填的 0 转成该负哨兵，domain 侧转回 0；proposal
// 等 partial 调用方保持零值=未传语义，互不干扰。哨兵仅存在于内存转换瞬间，
// 绝不落库。
const ScoreThresholdResetSentinel float32 = -1

// RerankingResetSentinel / RerankModelResetSentinel / JudgeModelResetSentinel 是
// MergeUpdate 对字符串字段「显式清空」的编码：partial 合并以零值表示"未提供"，
// 但 reranking/rerank_model/judge_model 的 "" 是合法关闭值。handler PATCH（整体
// 替换契约）把用户显式传的 "" 转成这些 NUL 前缀哨兵，domain 侧转回 ""；proposal
// 等 partial 调用方保持零值=未传语义，互不干扰。哨兵仅存在于内存转换瞬间，绝不
// 落库（NUL 字节也保证不会与任何真实 JSONB 值碰撞）。
const (
	RerankingResetSentinel   = "\x00rerank_reset"
	RerankModelResetSentinel = "\x00rerank_model_reset"
	JudgeModelResetSentinel  = "\x00judge_model_reset"

	// RerankScoringInstructionsResetSentinel / JudgeScoringInstructionsResetSentinel
	// 同上，作用于两条评分指令字段（handler PATCH 显式清空指令时编码）。
	RerankScoringInstructionsResetSentinel = "\x00rerank_scoring_instructions_reset"
	JudgeScoringInstructionsResetSentinel  = "\x00judge_scoring_instructions_reset"
)

// NewWorkspace constructs a Workspace, applying defaults to cfg and validating it.
// Callers receive ErrInvalidEmbeddingModel / ErrInvalidQueryMode on bad input.
func NewWorkspace(name, description string, cfg WorkspaceConfig, defaultChunkSize, defaultTopK int) (*Workspace, error) {
	cfg = applyDefaults(cfg, defaultChunkSize, defaultTopK)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Workspace{
		Name:        name,
		Description: description,
		Config:      cfg,
	}, nil
}

// Validate checks that QueryMode, ChunkingStrategy, and the rerank settings
// fall within the allowed sets. Embedding model existence in the global
// catalogue is validated by the application layer (port.ModelExists) after
// this pure structural check — domain stays free of I/O.
// The embedding model must be explicitly configured: it is the only source of
// truth (no static or dynamic default), so an empty value is rejected.
// The check is deliberately last — structural errors (query mode, chunking,
// rerank, threshold) keep priority over the missing-field error.
//
// 表驱动按序校验，命中即返回（顺序即优先级）。范围校验（ScoreThreshold/TopK/
// RerankTopK）无条件执行（即使重排关闭）：TopK 必须落在 [1, MaxRAGTopK]
// （0 是未初始化，经 applyDefaults 填补后到达这里必然 ≥1）；RerankTopK 落在
// [0, MaxRerankTopK]（0 = 跟随 TopK，合法）。上限与 proto QueryRequest.topK /
// workspace 契约同步。每行谓词收敛为单个判定点以控制圈复杂度。
func (c WorkspaceConfig) Validate() error {
	missingRerankModel := c.Reranking == "builtin-score-v1" && c.RerankModel == ""
	for _, check := range []struct {
		ok  bool
		err error
	}{
		{AllowedQueryModes[c.QueryMode], ErrInvalidQueryMode},
		{AllowedChunkingStrategies[c.ChunkingStrategy], ErrInvalidChunkingStrategy},
		{ValidRerankIdentity(c.Reranking), ErrInvalidRerankIdentity},
		{inRange(c.ScoreThreshold, 0, 1), ErrInvalidScoreThreshold},
		{inRange(c.TopK, 1, constants.MaxRAGTopK), ErrInvalidTopK},
		{inRange(c.RerankTopK, 0, constants.MaxRerankTopK), ErrInvalidRerankTopK},
		{withinRuneLimit(c.RerankScoringInstructions, constants.MaxRerankScoringInstructionsRunes) &&
			withinRuneLimit(c.JudgeScoringInstructions, constants.MaxJudgeScoringInstructionsRunes),
			ErrInvalidScoringInstructions},
		{c.EmbeddingModel != "", ErrEmbeddingModelRequired},
		{!missingRerankModel, ErrRerankModelRequired},
	} {
		if !check.ok {
			return check.err
		}
	}
	return nil
}

// inRange 判断数值落在闭区间 [lo, hi]。泛型约束覆盖 Validate 用到的 int/float32，
// 把 "v<lo || v>hi" 两个判定点收敛为单一谓词，避免散落 if 抬高圈复杂度。
func inRange[T int | float32](v, lo, hi T) bool {
	return v >= lo && v <= hi
}

// withinRuneLimit 判断字符串 rune 长度不超过上限；空串（0 rune）恒合法——
// 存量 workspace JSONB 无指令键读回空，运行时回落内置评分 prompt。
func withinRuneLimit(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max
}

// ValidRerankIdentity 校验 rerank identity 结构：内部策略（""/builtin）直接
// 合法；外部 provider 必须为 "provider:model" 形式（model 非空），provider
// 存在性由 application 层目录校验（domain 不做 I/O）。
func ValidRerankIdentity(identity string) bool {
	provider, model := SplitRerankIdentity(identity)
	if AllowedRerankIdentities[provider] {
		return true
	}
	return model != ""
}

func applyDefaults(c WorkspaceConfig, defaultChunkSize, defaultTopK int) WorkspaceConfig {
	if c.QueryMode == "" {
		c.QueryMode = DefaultQueryMode
	}
	if c.ChunkSize <= 0 {
		c.ChunkSize = defaultChunkSize
	}
	if c.ChunkOverlap <= 0 {
		c.ChunkOverlap = DefaultChunkOverlap
	}
	if c.TopK <= 0 {
		c.TopK = defaultTopK
	}
	if c.ChunkingStrategy == "" {
		c.ChunkingStrategy = DefaultChunkingStrategy
	}
	// 新建 workspace 预填内置推荐指令文本（可编辑；空指令运行时回落内置 prompt）。
	if c.RerankScoringInstructions == "" {
		c.RerankScoringInstructions = DefaultRerankScoringInstructions
	}
	if c.JudgeScoringInstructions == "" {
		c.JudgeScoringInstructions = DefaultJudgeScoringInstructions
	}
	return c
}

// MergeUpdate returns the result of applying a partial update to the current
// config. It enforces immutability of embedding_model / chunk_size / chunk_overlap
// and validates the resulting query_mode and rerank settings.
func (c WorkspaceConfig) MergeUpdate(partial WorkspaceConfig) (WorkspaceConfig, error) {
	out := c
	if err := c.applyImmutableSettings(partial); err != nil {
		return c, err
	}
	if partial.QueryMode != "" {
		if !AllowedQueryModes[partial.QueryMode] {
			return c, ErrInvalidQueryMode
		}
		out.QueryMode = partial.QueryMode
	}
	if partial.TopK > 0 {
		if partial.TopK > constants.MaxRAGTopK {
			return c, ErrInvalidTopK
		}
		out.TopK = partial.TopK
	}
	merged, err := out.applyRerankSettings(partial)
	if err != nil {
		return c, err
	}
	return merged, nil
}

// applyImmutableSettings rejects updates to fields that are fixed after
// workspace creation.
func (c WorkspaceConfig) applyImmutableSettings(partial WorkspaceConfig) error {
	if partial.EmbeddingModel != "" && partial.EmbeddingModel != c.EmbeddingModel {
		return ErrEmbeddingModelImmutable
	}
	if partial.ChunkSize > 0 && partial.ChunkSize != c.ChunkSize {
		return ErrChunkSizeImmutable
	}
	if partial.ChunkOverlap > 0 && partial.ChunkOverlap != c.ChunkOverlap {
		return ErrChunkOverlapImmutable
	}
	if partial.ChunkingStrategy != "" && partial.ChunkingStrategy != c.ChunkingStrategy {
		return ErrChunkingStrategyImmutable
	}
	return nil
}

// applyRerankSettings merges the rerank fields, validating the identity and
// score threshold before applying.
func (c WorkspaceConfig) applyRerankSettings(partial WorkspaceConfig) (WorkspaceConfig, error) {
	out := c
	if reranking, ok := mergeResetField(out.Reranking, partial.Reranking, RerankingResetSentinel); ok {
		if !ValidRerankIdentity(reranking) {
			return c, ErrInvalidRerankIdentity
		}
		out.Reranking = reranking
	}
	if partial.ScoreThreshold > 0 {
		if partial.ScoreThreshold > 1 {
			return c, ErrInvalidScoreThreshold
		}
		out.ScoreThreshold = partial.ScoreThreshold
	} else if partial.ScoreThreshold == ScoreThresholdResetSentinel {
		// 显式 0 重置：handler PATCH 整体替换契约把 0 编码为哨兵，partial
		// 语义下零值=未传，0 只能经哨兵显式写入，防止"设了关不掉"。
		out.ScoreThreshold = 0
	}
	if partial.RerankTopK > 0 {
		if partial.RerankTopK > constants.MaxRerankTopK {
			return c, ErrInvalidRerankTopK
		}
		out.RerankTopK = partial.RerankTopK
	}
	if err := mergeBoundedStrings(&out, partial); err != nil {
		return c, err
	}
	return out, nil
}

// mergeBoundedStrings 归并「可清空字符串」字段的 partial 合并：rerank_model /
// judge_model 与两条评分指令同构（哨兵清空 / 非空覆盖 / 零值保留）。评分指令带
// rune 上限——Update 路径不经过 Validate，merge 内同步做长度校验（越界
// fail-closed）；maxRunes=0 表示不设上限（模型名目录校验不在此处）。
func mergeBoundedStrings(out *WorkspaceConfig, partial WorkspaceConfig) error {
	for _, f := range []struct {
		dst      *string
		partial  string
		sentinel string
		maxRunes int
	}{
		{&out.RerankModel, partial.RerankModel, RerankModelResetSentinel, 0},
		{&out.JudgeModel, partial.JudgeModel, JudgeModelResetSentinel, 0},
		{&out.RerankScoringInstructions, partial.RerankScoringInstructions, RerankScoringInstructionsResetSentinel, constants.MaxRerankScoringInstructionsRunes},
		{&out.JudgeScoringInstructions, partial.JudgeScoringInstructions, JudgeScoringInstructionsResetSentinel, constants.MaxJudgeScoringInstructionsRunes},
	} {
		v, ok := mergeResetField(*f.dst, f.partial, f.sentinel)
		if !ok {
			continue
		}
		if f.maxRunes > 0 && !withinRuneLimit(v, f.maxRunes) {
			return ErrInvalidScoringInstructions
		}
		*f.dst = v
	}
	return nil
}

// mergeResetField applies the reset-sentinel merge for a string config field:
// the sentinel clears to "", non-empty overrides, zero value preserves current.
// The sentinel branch comes first so a NUL-prefixed sentinel never reaches a
// non-empty validation path. Returns the merged value and whether an update
// was requested (sentinel or explicit non-empty).
func mergeResetField(current, partial, sentinel string) (string, bool) {
	switch {
	case partial == sentinel:
		return "", true
	case partial != "":
		return partial, true
	default:
		return current, false
	}
}

// Rename mutates Name; reserved for future when name editing is allowed.
func (w *Workspace) Rename(name string) {
	w.Name = name
}

// UpdateDescription mutates Description on the aggregate.
func (w *Workspace) UpdateDescription(desc string) {
	w.Description = desc
}

// UpdateConfig replaces the workspace config with the supplied (already-validated) cfg.
func (w *Workspace) UpdateConfig(cfg WorkspaceConfig) {
	w.Config = cfg
}
