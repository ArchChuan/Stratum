import { z } from 'zod';

import {
  skillRevisionSchema, skillSchema, skillWorkspaceSchema,
  type CreateSkillDraftPayload, type Skill, type SkillRequirements, type SkillRevision, type SkillWorkspace,
} from '../model/skill';

import api from '@/services/client';

export const skillApi = {
  list: async (): Promise<Skill[]> => z.array(skillSchema).parse((await api.get('/skills')).data?.skills ?? []),
  get: async (id: string): Promise<SkillWorkspace> => skillWorkspaceSchema.parse((await api.get(`/skills/${id}`)).data),
  createDraft: async (data: CreateSkillDraftPayload): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post('/skills', data)).data),
  getWorkspace: async (id: string): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.get(`/skills/${id}/workspace`)).data),
  updateCapability: async (id: string, data: { goal: string; whenToUse: string; inputSpec?: string; outputSpec?: string }): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.patch(`/skills/${id}/draft/capability`, data)).data),
  updateActivation: async (id: string, data: { name: string; description: string; inputSchema: Record<string, unknown>; outputSchema: Record<string, unknown>; confirmed: boolean }): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.patch(`/skills/${id}/draft/activation`, data)).data),
  updateInstructions: async (id: string, data: { instructions: string; requirements: SkillRequirements }): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.patch(`/skills/${id}/draft/instructions`, data)).data),
  delete: (id: string) => api.delete(`/skills/${id}`),
  publish: async (id: string): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.post(`/skills/${id}/publish`)).data),
};
