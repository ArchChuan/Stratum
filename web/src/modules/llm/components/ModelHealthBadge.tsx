import { Tag } from 'antd';

// 模型健康状态徽章：取值与后端 infrastructure.ModelHealth 一致。
// 未提供 health（尚未探活）时展示灰色"未探活"，不推断为健康。
export type ModelHealth = '' | 'healthy' | 'degraded' | 'unhealthy' | 'half_open';

const HEALTH_META: Record<Exclude<ModelHealth, ''>, { color: string; label: string }> = {
  healthy: { color: 'success', label: '健康' },
  degraded: { color: 'warning', label: '降级' },
  unhealthy: { color: 'error', label: '不可用' },
  half_open: { color: 'processing', label: '探测中' },
};

export function ModelHealthBadge({ health }: { health?: string }) {
  const meta = health ? HEALTH_META[health as ModelHealth] : undefined;
  if (!meta) return <Tag>未探活</Tag>;
  return <Tag color={meta.color}>{meta.label}</Tag>;
}
