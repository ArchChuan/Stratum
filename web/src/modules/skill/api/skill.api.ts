import { z } from 'zod';

import {
  skillRevisionSchema, skillSchema, skillWorkspaceSchema,
  type CreateSkillPayload, type Skill, type SkillRevision, type SkillWorkspace,
} from '../model/skill';

import api from '@/services/client';
import type { PublishSkillDraftRequest, SaveSkillDraftRequest, SkillRevisionsResponse, UpdateSkillRequest } from '@/services/gen/skill';

export const skillApi = {
  list: async (): Promise<Skill[]> => z.array(skillSchema).parse((await api.get('/skills')).data?.skills ?? []),
  get: async (id: string): Promise<SkillWorkspace> => skillWorkspaceSchema.parse((await api.get(`/skills/${id}`)).data),
  createSkill: async (data: CreateSkillPayload): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post('/skills', data)).data),
  getWorkspace: async (id: string): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.get(`/skills/${id}/workspace`)).data),
  // updateSkill: 保存即生效——基于当前生效版本派生新版本，无发布步骤。
  // expectedContentHash 由前端从当前 active.contentHash 取得，并发编辑时后端返回 409。
  updateSkill: async (id: string, data: UpdateSkillRequest): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.patch(`/skills/${id}`, data)).data),
  // saveDraft: 保存草稿,不立即生效;发布后才成为当前生效版本。
  // expectedContentHash 取当前 active.contentHash,并发编辑时后端返回 409。
  saveDraft: async (id: string, data: SaveSkillDraftRequest): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post(`/skills/${id}/draft`, data)).data),
  // publishDraft: 将草稿提升为新的生效版本(版本号分配、旧版降级、指针重指)。
  publishDraft: async (id: string, data: PublishSkillDraftRequest): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post(`/skills/${id}/publish`, data)).data),
  // discardDraft: 删除草稿,返回当前生效版本供表单回填;幂等。
  discardDraft: async (id: string): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.delete(`/skills/${id}/draft`)).data),
  setEditors: (id: string, editorIds: string[]) =>
    api.put(`/skills/${id}/editors`, { editorIds }),
  delete: (id: string) => api.delete(`/skills/${id}`),
  // listRevisions: 版本历史，当前生效版本标记 isCurrent。
  listRevisions: async (id: string): Promise<SkillRevision[]> => {
    const data = (await api.get(`/skills/${id}/revisions`)).data as SkillRevisionsResponse;
    return (data?.revisions ?? []).map((revision) => skillRevisionSchema.parse(revision));
  },
  // rollback: 将生效指针指回历史已发布版本，立即生效、不产生新版本。
  rollback: (id: string, revisionId: string) =>
    api.post(`/skills/${id}/rollback`, { revisionId }),
};
