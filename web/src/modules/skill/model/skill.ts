import { z } from 'zod';

const jsonObjectSchema = z.record(z.unknown());

export const skillSchema = z.object({
  id: z.string(), name: z.string(), description: z.string().optional().default(''),
  status: z.string().optional().default('draft'), activeRevisionId: z.string().optional(),
  draftRevisionId: z.string().optional(), created_at: z.string().optional(), updated_at: z.string().optional(),
}).passthrough();
export type Skill = z.infer<typeof skillSchema>;
export type SkillConfig = never;
export type SkillType = never;

export const skillProductSchema = skillSchema;
export type SkillProduct = Skill;

export const skillRequirementsSchema = z.object({
  mcpToolIds: z.array(z.string()).default([]),
  knowledgeWorkspaceIds: z.array(z.string()).default([]),
  memoryScopes: z.array(z.string()).default([]),
});
export type SkillRequirements = z.infer<typeof skillRequirementsSchema>;

export const skillRevisionSchema = z.object({
  id: z.string(), skillId: z.string(), revisionNo: z.number().optional(), status: z.string(),
  capability: jsonObjectSchema.default({}), activationContract: jsonObjectSchema.default({}),
  instructions: z.string().default(''), requirements: skillRequirementsSchema.default({
    mcpToolIds: [], knowledgeWorkspaceIds: [], memoryScopes: [],
  }), publishChecks: jsonObjectSchema.optional(),
}).passthrough();
export type SkillRevision = z.infer<typeof skillRevisionSchema>;
export type SkillVersion = SkillRevision;

export const skillWorkspaceSchema = z.object({ skill: skillProductSchema, draft: skillRevisionSchema }).passthrough();
export type SkillWorkspace = z.infer<typeof skillWorkspaceSchema>;

export interface SkillFormValues {
  name: string;
  goal: string;
  whenToUse: string;
  sampleInput: string;
  expectedOutput: string;
  instructions: string;
  mcpToolIDs?: string;
  knowledgeWorkspaceIDs?: string;
  memoryScopes?: string[];
}

export interface CreateSkillDraftPayload {
  name: string;
  goal: string;
  whenToUse: string;
  sampleInput: string;
  expectedOutput: string;
  instructions: string;
  requirements: SkillRequirements;
}

const lines = (value?: string) => (value || '').split('\n').map((item) => item.trim()).filter(Boolean);

export const buildCreateSkillDraftPayload = (values: SkillFormValues): CreateSkillDraftPayload => ({
  name: values.name, goal: values.goal, whenToUse: values.whenToUse,
  sampleInput: values.sampleInput, expectedOutput: values.expectedOutput, instructions: values.instructions,
  requirements: {
    mcpToolIds: lines(values.mcpToolIDs), knowledgeWorkspaceIds: lines(values.knowledgeWorkspaceIDs),
    memoryScopes: values.memoryScopes || [],
  },
});
