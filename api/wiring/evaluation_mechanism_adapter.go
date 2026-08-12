package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// mechanismEvaluationAdapter 以模型档案（model_profiles 族档位）为被测对象
// 实现 evaluation 的 ResourceAdapter（机制基线设计 §5）：
//   - revision 是「指纹」虚拟 revision：ref.RevisionID 必须等于档案当前
//     指纹，档案变更后旧指纹立即失效（ErrRevisionNotPublished），迫使
//     矩阵重评测，档案版本化回退由此成立；
//   - case 执行用档案声明的 EnrichModel + 对应模板调用 LLM（Temperature=0
//     保证评测确定性），模型/模板缺失时 fail closed，禁止静默回退默认值。
//
// model_profiles 是 public 全局表（阶段2裁决），与租户无依附，tenantID
// 仅透传不参与查询。
// mechanismProfileReader 是 adapter 对档案服务的依赖收窄（消费方定义
// 小接口，*mechanismapp.Service 天然实现，测试用 stub）。
type mechanismProfileReader interface {
	GetByFamilyKey(ctx context.Context, familyKey string) (mechanismdomain.Profile, error)
}

type mechanismEvaluationAdapter struct {
	profiles  mechanismProfileReader
	completer llmgatewaydomain.LLMCompleter
}

// matrixCaseInput 是矩阵基准集 case 的 input 结构（{"template","input"}）。
type matrixCaseInput struct {
	Template string `json:"template"` // BaselinePrompts 六键之一
	Input    string `json:"input"`    // 评测输入文本
}

func (a *mechanismEvaluationAdapter) profileByRef(
	ctx context.Context, ref evaldomain.ResourceRef,
) (mechanismdomain.Profile, error) {
	profile, err := a.profiles.GetByFamilyKey(ctx, ref.ResourceID)
	if err != nil {
		return mechanismdomain.Profile{}, fmt.Errorf("mechanism evaluation adapter: load profile %s: %w", ref.ResourceID, err)
	}
	if ref.RevisionID != profile.Fingerprint {
		return mechanismdomain.Profile{}, fmt.Errorf(
			"%w: mechanism profile %s fingerprint changed, re-run matrix evaluation",
			evaldomain.ErrRevisionNotPublished, ref.ResourceID,
		)
	}
	return profile, nil
}

func (a *mechanismEvaluationAdapter) ResolveRevision(
	ctx context.Context, _ string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	profile, err := a.profileByRef(ctx, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	return evaldomain.ResourceRevision{
		ID:           profile.Fingerprint,
		ResourceKind: evaldomain.ResourceKindMechanism,
		ResourceID:   profile.FamilyKey,
		Source:       evaldomain.RevisionSourceManual,
		Status:       evaldomain.RevisionStatusPublished,
		ContentHash:  profile.Fingerprint,
		PayloadRef:   "profile:" + profile.FamilyKey,
		PayloadHash:  profile.Fingerprint,
		SafeSummary:  mechanismProfileSafeSummary(profile),
		CreatedBy:    profile.CreatedBy,
		CreatedAt:    profile.UpdatedAt,
	}, nil
}

func (a *mechanismEvaluationAdapter) SafeSummary(
	ctx context.Context, _ string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	profile, err := a.profileByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	return mechanismProfileSafeSummary(profile), nil
}

func (a *mechanismEvaluationAdapter) ExecuteRevision(
	ctx context.Context, _ string, _ string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	var input matrixCaseInput
	payload, err := json.Marshal(testCase.Input)
	if err != nil {
		return evalport.ExecutionResult{}, fmt.Errorf("mechanism evaluation adapter: encode case input: %w", err)
	}
	if err := json.Unmarshal(payload, &input); err != nil || input.Template == "" || input.Input == "" {
		return evalport.ExecutionResult{}, errors.New("mechanism evaluation adapter: case input must be {\"template\",\"input\"}")
	}
	profile, err := a.profileByRef(ctx, ref)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	model := profile.Baseline.Models.EnrichModel
	if model == "" {
		return evalport.ExecutionResult{}, errors.New("mechanism evaluation adapter: profile enrich model is empty, refusing silent fallback")
	}
	template := mechanismTemplateByKey(profile.Baseline.Prompts, input.Template)
	if template == "" {
		return evalport.ExecutionResult{}, fmt.Errorf("mechanism evaluation adapter: unknown template key %q", input.Template)
	}
	if a.completer == nil {
		return evalport.ExecutionResult{}, errors.New("mechanism evaluation adapter: no LLM completer configured")
	}
	start := time.Now()
	response, err := a.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:       model,
		Temperature: 0, // 评测确定性：同一输入同一输出
		MaxTokens:   constants.MatrixMaxTokens,
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: template},
			{Role: "user", Content: input.Input},
		},
	})
	if err != nil {
		return evalport.ExecutionResult{}, fmt.Errorf("mechanism evaluation adapter: complete %s: %w", model, err)
	}
	return evalport.ExecutionResult{
		Output:     response.Content,
		TraceID:    uuid.Must(uuid.NewV7()).String(),
		Tokens:     response.Usage.TotalTokens,
		CostUSD:    0,
		DurationMs: int(time.Since(start).Milliseconds()),
	}, nil
}

// mechanismTemplateByKey 按基准集 case 的 template 键取档案模板；未知键
// 返回空串（调用方 fail closed）。
func mechanismTemplateByKey(prompts mechanismdomain.BaselinePrompts, key string) string {
	switch key {
	case "memory_extraction":
		return prompts.MemoryExtraction
	case "memory_summary":
		return prompts.MemorySummary
	case "memory_enrichment":
		return prompts.MemoryEnrichment
	case "memory_summarize":
		return prompts.MemorySummarize
	case "memory_supersede":
		return prompts.MemorySupersede
	case "compaction":
		return prompts.Compaction
	default:
		return ""
	}
}

// mechanismProfileSafeSummary 是档案的脱敏摘要（过 resource.go 敏感键校验：
// 键名必须避开 token 边界——family_key 因 "key" token 命中敏感集合，改用
// family；display_name/status/version/fingerprint/enrich_model/summary_model
// 均安全，值不含 token/密码形态）。
func mechanismProfileSafeSummary(profile mechanismdomain.Profile) map[string]any {
	return map[string]any{
		"family":        profile.FamilyKey,
		"display_name":  profile.DisplayName,
		"status":        profile.Status,
		"version":       profile.Version,
		"fingerprint":   profile.Fingerprint,
		"enrich_model":  profile.Baseline.Models.EnrichModel,
		"summary_model": profile.Baseline.Models.SummaryModel,
	}
}
