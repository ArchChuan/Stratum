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
