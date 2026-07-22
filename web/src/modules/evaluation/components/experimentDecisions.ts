import type { ExperimentSummary } from '../model/evaluation';

const gateLabels: Record<string, string> = {
  quality: '质量', cost: '成本', latency: '时延', error_rate: '错误率', security: '安全',
};

export const promotionBlockReason = (experiment: ExperimentSummary): string => {
  if (experiment.safety_stopped) return '安全停止已触发，必须先回滚并复核证据';
  if (!experiment.gates) return '门禁证据缺失，无法晋级';
  const unavailable = Object.entries(experiment.gates).find(([, value]) => value === 'not_applicable');
  if (unavailable) return `${gateLabels[unavailable[0]]}证据依赖暂不可用，无法晋级`;
  const failed = Object.entries(experiment.gates).find(([, value]) => value === 'failed');
  if (failed) return `${gateLabels[failed[0]]}门禁违反，无法晋级`;
  if (experiment.gates.quality === 'pending') return '质量样本量不足，无法晋级';
  if (experiment.gates.latency === 'pending') return '观测时长不足，无法晋级';
  const pending = Object.entries(experiment.gates).find(([, value]) => value === 'pending');
  if (pending) return `${gateLabels[pending[0]]}证据仍在收集，无法晋级`;
  if (experiment.recommendation !== 'promote') return '系统建议继续观察，尚未建议晋级';
  return '';
};

export type ExperimentAction = 'pause' | 'promote' | 'rollback';
export const experimentActions = (status: string): ExperimentAction[] => status === 'running'
  ? ['pause', 'promote', 'rollback'] : status === 'paused' ? ['rollback'] : [];
