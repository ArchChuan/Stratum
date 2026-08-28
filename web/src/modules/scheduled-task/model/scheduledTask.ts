import { z } from 'zod';

const goSlice = <T extends z.ZodTypeAny>(item: T) => z.preprocess(
  (v) => (Array.isArray(v) ? v : []),
  z.array(item),
);

export const scheduledTaskSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  workflowId: z.string().min(1),
  versionId: z.string().min(1),
  inputTemplate: z.record(z.string(), z.unknown()).default({}),
  cronExpr: z.string().min(1),
  enabled: z.boolean(),
  nextFireAt: z.string(),
  lastRunAt: z.string().optional(),
  lastRunStatus: z.string(),
  lastErrorMessage: z.string().optional(),
  createdBy: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  // 展示用引用实体名称（默认展示 name，原始 id 可 hover 悬浮）：引用实体
  // 已删除或未解析时后端缺省，前端回退展示原始 id。
  workflowName: z.string().optional(),
  versionNo: z.number().int().nonnegative().optional(),
  versionName: z.string().optional(),
}).strict();
export type ScheduledTask = z.infer<typeof scheduledTaskSchema>;

export const scheduledTaskPageSchema = z.object({
  tasks: goSlice(scheduledTaskSchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  pageSize: z.number().int().positive(),
});
export type ScheduledTaskPage = z.infer<typeof scheduledTaskPageSchema>;

export interface CreateScheduledTaskInput {
  name: string;
  workflowId: string;
  versionId: string;
  inputTemplate: Record<string, unknown>;
  cronExpr: string;
}

export type UpdateScheduledTaskInput = CreateScheduledTaskInput;
