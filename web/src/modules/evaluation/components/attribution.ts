import type { EvaluationRun } from '../model/evaluation';

type RunResult = EvaluationRun['results'][number];

// §6.3 失败归因口径（报告 buildReportRows 与 RunAttributionPanel 失败聚类共用）：
// 输出断言失败（failure_reason）或过程断言失败（process_pass=false）都算可归因失败。
export const isFailedAttributionCase = (r: RunResult): boolean =>
  !r.passed && (Boolean(r.failure_reason) || r.process_pass === false);

// 归因键：输出断言失败取 failure_reason；过程失败取 process_failure，无具体值时兜底 'process:failed'。
export const reasonOf = (r: RunResult): string =>
  r.failure_reason || (r.process_pass === false ? (r.process_failure || 'process:failed') : '');
