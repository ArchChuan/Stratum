import type { ModelCapability } from './llm';

export interface GroupedModelOption {
  provider: string;
  models: {
    value: string;
    label: string;
    // 模型能力标签（chat/embedding/vision/tool_use/reasoning），供模型选择器展示。
    // 可选：buildGroupedModels 总是产出（空数组兜底）；外部构造者可省略。
    capabilities?: ModelCapability[];
    reasoning: boolean;
    contextWindow?: number;
    maxTokens?: number;
    // 运行时健康状态（healthy/degraded/unhealthy/half_open）；缺失 = 未探活。
    health?: string;
    // 模型是否启用（平台模型管理开关）。enabled=false 时选择器显示但禁用不可选。
    enabled?: boolean;
  }[];
}

// 按厂商聚合模型下拉选项（Agent 表单、平台参数、知识库等所有模型选择器共用）。
// capabilities 可选：缺失时按默认推理名单推断（见 isReasoningModel），
// 避免调用方为拿能力标志额外加一次请求。health 透传模型运行时状态，
// 供选择器按不可用模型禁用/打标（缺失时不推断健康）。enabled 透传模型
// 启用开关，供选择器对禁用模型"显示但禁用不可选"（fail-closed）。
export const buildGroupedModels = (
  models: Array<{
    providerId: string;
    name: string;
    displayName?: string;
    capabilities?: ModelCapability[];
    contextWindow?: number;
    maxTokens?: number;
    health?: string;
    enabled?: boolean;
  }>,
  providers: Array<{ id: string; name: string }>,
): GroupedModelOption[] => {
  const providerMap = new Map(providers.map((p) => [p.id, p.name]));
  const grouped = new Map<string, GroupedModelOption['models']>();
  for (const m of models) {
    const providerName = providerMap.get(m.providerId) || m.providerId;
    if (!grouped.has(providerName)) grouped.set(providerName, []);
    grouped.get(providerName)!.push({
      value: m.name,
      label: m.displayName || m.name,
      capabilities: m.capabilities ?? [],
      reasoning: isReasoningModel(m),
      contextWindow: m.contextWindow,
      maxTokens: m.maxTokens,
      health: m.health,
      enabled: m.enabled,
    });
  }
  return Array.from(grouped.entries()).map(([provider, modelOptions]) => ({
    provider,
    models: modelOptions,
  }));
};

// 推理模型判定：优先用 catalog/DB 返回的 capabilities，缺失时按命名规则兜底
// （o1/o3/o4/deepseek-reasoner/qwq 等）。与后端 pkg/constants 的
// reasoning 能力标注保持一致；未知模型前端默认视为非推理（fail-closed）。
const isReasoningModel = (
  m: { name: string; capabilities?: string[] },
): boolean => {
  if (m.capabilities?.length) {
    return m.capabilities.includes('reasoning');
  }
  return /^(o1|o3|o4|deepseek-reasoner|qwq)/i.test(m.name);
};
