# 机制基线（Mechanism Baseline）设计

状态：已裁决，实施中。本文档锚定产品设计决策，实施以本文件为准。

## 1. 目标与边界

机制面（平台固定、租户零感知）的可调参数从硬编码/env/Nacos 收敛到存储（DB），
获得版本化、回退、评测迭代能力。业务面（租户调优空间）不在本文档范围。

**边界**：

- 机制基线 = 全局共享引用语义（改一处全体生效），放 public schema，不复制进租户 schema
- 管理入口依附默认租户（`public.tenants.is_default`），权限继承租户角色体系
- 评测闭环复用既有 evaluation 基础设施（suite/run/judge/多维指标）

## 2. 存储设计

### model_profiles 表（迁移 034，public schema）

```sql
CREATE TABLE IF NOT EXISTS public.model_profiles (
    id            TEXT PRIMARY KEY,
    family_key    TEXT NOT NULL,            -- 族键，如 "qwen"
    display_name  TEXT NOT NULL,
    model_matcher JSONB NOT NULL,           -- {"family_prefixes": ["qwen-turbo","qwen-max",...]}
    baseline      JSONB NOT NULL,           -- 机制基线（见下）
    fingerprint   TEXT NOT NULL,            -- 档案指纹（窗口+能力+基线+评测日期）
    version       INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL DEFAULT 'active',  -- active | draft
    created_by    TEXT NOT NULL DEFAULT 'seed',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_profiles_family ON public.model_profiles (family_key);
```

### baseline JSONB 键（机制参数全集）

```jsonc
{
  "prompts": {
    "memory_extraction": "…",   // 原 embedded llm_extractor 模板
    "memory_summary": "…",      // 原 history_summarizer 英文模板
    "memory_enrichment": "…",   // 原 enricher_prompt.go enrichmentPrompt
    "compaction": "…"           // 原 history_compactor.go compactionSystemPrompt
  },
  "models": {
    "enrich_model": "qwen-turbo",   // 原 env EnrichModel
    "summary_model": "qwen-plus"    // 原 env SummaryModel
  },
  "recall": {
    "recall_top_k": null,           // 原注册表假参数，未接线前保持 null
    "fact_injection_top_n": null,
    "long_term_top_k": null
  }
}
```

### 解析优先级（全部消费路径统一）

```
1. model_profiles（按模型族匹配，懒加载按需分裂）
2. embedded 种子（代码轨，冷启动兜底 = 现状硬编码值）
3. 缺失 profile 时回退 2，禁止 1 失败即降级为错误
```

## 3. 消费路径改造

| 现状 | 改后 |
|---|---|
| `pipeline/enricher_prompt.go` 硬编码常量 | tmpl 由基线 resolver 注入（已有 `formatXxxPrompt(tmpl,...)` 兜底） |
| `workers/llm_extractor.go` 硬编码中文抽取模板 | 经 `makeLLMExtractResolver` 注入 |
| `workers/llm_superseder.go` 硬编码判断模板 | 经 resolver 注入 |
| `workers/history_summarizer.go` 英文模板 | 经 resolver 注入 |
| `capability/history_compactor.go:64` compactionSystemPrompt | 构造时从基线读取 |
| env `EnrichModel/SummaryModel` | 基线 `models.*` 优先，env 兜底 |

注入形态：wiring 构造 `BaselineResolver`（DB→embedded 兜底），消费方经
`func(ctx, model) (Baseline, error)` 取基线；worker 场景按 tenant 解析。
缓存：profile 行数个位数，启动时加载 + 变更后 invalidate（复用 Platform 缓存模式）。

## 4. 管理面：默认租户依附

默认租户三重身份：

1. **管理入口**：默认租户产品端内「平台工作台」入口（角色条件渲染，不建独立 admin 应用）
2. **真实业务数据场**：机制基线打磨离不开真实业务场景和数据——基准集冷启动
   从默认租户真实流量采样沉淀（shadow 模式），评测素材长自真实数据
3. **验证场**：基线新版本先在默认租户真实流量 shadow 对比，再推广全平台

推论：默认租户必须承载真实业务（真实对话/memory/RAG 流量），不能是空壳；
机制参数对默认租户普通成员仍零感知，仅平台运营以其数据沉淀评测资产。

- **权限**：继承租户角色体系——owner/admin 可管理机制基线；member 只读不可见入口。
  机制基线发布 = 平台级动作：审计留痕（audit_events）
- **API**：`GET/PUT /mechanism/baselines`（默认租户上下文 + RequireTenantRole(admin)）
- **发布流程**：draft → active 两态（评测采纳后置 active；快速修改允许直接 active，留审计）

## 5. 与评测闭环的关系（阶段 3）

- 机制基线进评测闭环：基准集 × 模型档案矩阵评测（复用 evaluation suite/run/judge）
- 采纳 = 多维帕累托（fidelity/cost/perf，评测报告已有 Tokens/CostUSD/DurationMs 雏形）
- 模型升级 → 档案指纹变化 → 重评测 → 档案版本化回退

## 6. 双轨资产治理

- 代码轨：embedded 种子 + 基准集 + 迁移（CI/CD 门禁）
- 数据轨：model_profiles 运行态真相（评测闭环产出）
- 部署处 seed 交汇：启动 provision 幂等写入（无 profile 的族自动建档，值为 embedded 种子）

## 7. 成功标准

1. 机制参数消费路径全部读 DB（DB 空时回退 embedded，行为与现状一致）
2. 默认租户 owner/admin 可管理机制基线，member 403，非默认租户 403
3. 变更审计留痕
4. `go vet && go test -short ./...` 全绿；迁移测试覆盖新表
5. 生产消费路径无回归（memory 管线、compaction 链路）

## 8. 明确不做（本阶段）

- 租户业务参数版本化（业务面，后续阶段）
- 评测矩阵工作台（阶段 3）
- 假参数接线（recall 三键保持 null，确认接线语义后单独立项）
- 调度参数迁出 Nacos（保持现状，独立立项）
