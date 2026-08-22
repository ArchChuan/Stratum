import type { ModelCapability } from '../model/llm';

import { ModelCapabilityTags } from './ModelCapabilityTags';
import { ModelHealthBadge } from './ModelHealthBadge';

// AntD Select showSearch 的过滤函数：Option children 是 ModelOptionLabel 组件
//（React element，AntD 默认 filterOption 无法对其取文本），改按 Option 的
// label（显示名）/ value（模型名）纯文本匹配，恢复模型搜索能力。
export const filterModelOption = (
  input: string,
  option?: { label?: unknown; value?: unknown },
): boolean => {
  const q = input.toLowerCase();
  return (
    String(option?.label ?? '').toLowerCase().includes(q) ||
    String(option?.value ?? '').toLowerCase().includes(q)
  );
};

interface ModelOptionLabelProps {
  label: string;
  // 模型能力标签；空数组不渲染标签（未知模型不臆测能力）。
  capabilities?: ModelCapability[];
  // 运行时健康状态；仅在 showHealth 时渲染（缺失 = 未探活，
  // ModelHealthBadge 内部降级为"未探活"Tag）。
  health?: string;
  // 是否显示健康徽章：ProviderModelSelect 等原有健康展示的选择器开启；
  // Agent 表单等仅新增能力标签的调用方关闭，避免"未探活"噪音。
  showHealth?: boolean;
}

// 模型下拉 Option 的统一 label：模型名 + 能力标签 +（可选）健康徽章。
// Agent 表单、平台参数、记忆配置等模型选择器共用，保证能力展示一致。
export function ModelOptionLabel({ label, capabilities, health, showHealth = false }: ModelOptionLabelProps) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
      {label}
      {capabilities && capabilities.length > 0 && <ModelCapabilityTags capabilities={capabilities} />}
      {showHealth && <ModelHealthBadge health={health} />}
    </span>
  );
}
