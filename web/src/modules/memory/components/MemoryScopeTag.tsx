import { Tag } from 'antd';

import { MEMORY_SCOPE_OPTIONS } from '@/constants';

// 记忆 scope 标注（#29：管理页对 user 级与 Agent 私有记忆做来源区分）。
// 文案复用 MEMORY_SCOPE_OPTIONS；未知值透传原始 scope，避免脏数据不可见。
export const MemoryScopeTag = ({ scope }: { scope?: string }) => {
  const label = MEMORY_SCOPE_OPTIONS.find((o) => o.value === scope)?.label ?? scope ?? '-';
  const color = scope === 'user' ? 'blue' : scope === 'agent' ? 'purple' : 'default';
  return <Tag color={color}>{label}</Tag>;
};
