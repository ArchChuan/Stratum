import { z } from 'zod';

import type { CreateSkillRequest as CreateSkillDraftPayload } from '@/services/gen/skill';

const jsonObjectSchema = z.record(z.unknown());

export const skillSchema = z.object({
  id: z.string(), name: z.string(), description: z.string().optional().default(''),
  status: z.string().optional().default('draft'), activeRevisionId: z.string().optional(),
  draftRevisionId: z.string().optional(), created_at: z.string().optional(), updated_at: z.string().optional(),
  // isSystem: 系统内置 skill（ID 前缀 builtin:）的资源属性。挂载已对普通 agent
  // 开放，资源写保护仍由后端 version_service 强制。
  isSystem: z.boolean().optional().default(false),
}).passthrough();
export type Skill = z.infer<typeof skillSchema>;
export type SkillConfig = never;
export type SkillType = never;

export const skillProductSchema = skillSchema;
export type SkillProduct = Skill;

export const skillRevisionSchema = z.object({
  id: z.string(), skillId: z.string(), revisionNo: z.number().optional(), status: z.string(),
  capability: jsonObjectSchema.default({}), activationContract: jsonObjectSchema.default({}),
  instructions: z.string().default(''), publishChecks: jsonObjectSchema.optional(),
}).passthrough();
export type SkillRevision = z.infer<typeof skillRevisionSchema>;
export type SkillVersion = SkillRevision;

export const skillWorkspaceSchema = z.object({
  skill: skillProductSchema,
  draft: skillRevisionSchema,
  editors: z.array(z.string()).default([]),
}).passthrough();
export type SkillWorkspace = z.infer<typeof skillWorkspaceSchema>;

export interface SkillFormValues {
  name: string;
  goal: string;
  whenToUse: string;
  sampleInput: string;
  expectedOutput: string;
  instructions: string;
  editors?: string[];
}

export type { CreateSkillDraftPayload };

export const buildCreateSkillDraftPayload = (values: SkillFormValues): CreateSkillDraftPayload => ({
  name: values.name, goal: values.goal, whenToUse: values.whenToUse,
  sampleInput: values.sampleInput, expectedOutput: values.expectedOutput, instructions: values.instructions,
  editors: values.editors || [],
});
