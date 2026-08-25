import { z } from 'zod';

import {
  skillRevisionSchema, skillSchema, skillWorkspaceSchema,
  type CreateSkillDraftPayload, type Skill, type SkillRevision, type SkillWorkspace,
} from '../model/skill';

import api from '@/services/client';
import type { UpdateSkillDraftRequest } from '@/services/gen/skill';

export const skillApi = {
  list: async (): Promise<Skill[]> => z.array(skillSchema).parse((await api.get('/skills')).data?.skills ?? []),
  get: async (id: string): Promise<SkillWorkspace> => skillWorkspaceSchema.parse((await api.get(`/skills/${id}`)).data),
  createDraft: async (data: CreateSkillDraftPayload): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post('/skills', data)).data),
  getWorkspace: async (id: string): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.get(`/skills/${id}/workspace`)).data),
  // updateDraft: 简化后的唯一草稿更新端点，收敛为 name/description/instructions 三字段。
  updateDraft: async (id: string, data: UpdateSkillDraftRequest): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.patch(`/skills/${id}/draft`, data)).data),
  setEditors: (id: string, editorIds: string[]) =>
    api.put(`/skills/${id}/editors`, { editorIds }),
  delete: (id: string) => api.delete(`/skills/${id}`),
  publish: async (id: string): Promise<SkillRevision> =>
    skillRevisionSchema.parse((await api.post(`/skills/${id}/publish`)).data),
};
