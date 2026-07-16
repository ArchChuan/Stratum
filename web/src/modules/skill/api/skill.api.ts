import { z } from 'zod';

import {
  skillSchema,
  skillTestResultSchema,
  skillVersionSchema,
  skillWorkspaceSchema,
  type CreateSkillDraftPayload,
  type Skill,
  type SkillFormValues,
  type SkillTestResult,
  type SkillVersion,
  type SkillWorkspace,
} from '../model/skill';

import api from '@/services/client';

export const skillApi = {
  list: async (): Promise<Skill[]> => {
    const res = await api.get('/skills');
    return z.array(skillSchema).parse(res.data?.skills ?? []);
  },
  get: async (id: string): Promise<Skill> => {
    const res = await api.get(`/skills/${id}`);
    return skillSchema.parse(res.data);
  },
  create: (data: SkillFormValues) => api.post('/skills', data),
  createDraft: async (data: CreateSkillDraftPayload): Promise<SkillWorkspace> => {
    const res = await api.post('/skills', data);
    return skillWorkspaceSchema.parse(res.data);
  },
  getWorkspace: async (id: string): Promise<SkillWorkspace> => {
    const res = await api.get(`/skills/${id}/workspace`);
    return skillWorkspaceSchema.parse(res.data);
  },
  updateCapability: async (
    id: string,
    data: { goal: string; whenToUse: string; inputSpec?: string; outputSpec?: string },
  ): Promise<SkillVersion> => {
    const res = await api.patch(`/skills/${id}/draft/capability`, data);
    return skillVersionSchema.parse(res.data);
  },
  updateContract: async (
    id: string,
    data: {
      toolName: string;
      description: string;
      inputSchema: Record<string, unknown>;
      outputSchema: Record<string, unknown>;
      callingGuidance?: string;
      confirmed: boolean;
    },
  ): Promise<SkillVersion> => {
    const res = await api.patch(`/skills/${id}/draft/contract`, data);
    return skillVersionSchema.parse(res.data);
  },
  updateImplementation: async (
    id: string,
    data: {
      mode: string;
      source: Record<string, unknown>;
      runtime?: Record<string, unknown>;
      permissions?: Record<string, unknown>;
      secretRefs?: string[];
    },
  ): Promise<SkillVersion> => {
    const res = await api.patch(`/skills/${id}/draft/implementation`, data);
    return skillVersionSchema.parse(res.data);
  },
  update: (id: string, data: SkillFormValues) => api.put(`/skills/${id}`, data),
  delete: (id: string) => api.delete(`/skills/${id}`),
  publish: async (id: string): Promise<SkillVersion> => {
    const res = await api.post(`/skills/${id}/publish`);
    return skillVersionSchema.parse(res.data);
  },
  test: async (id: string, input: unknown): Promise<SkillTestResult> => {
    const res = await api.post(`/skills/${id}/test`, { input });
    return skillTestResultSchema.parse(res.data);
  },
  testDraft: async (data: { skill: SkillFormValues; input: unknown }): Promise<SkillTestResult> => {
    const res = await api.post('/skills/test-draft', data);
    return skillTestResultSchema.parse(res.data);
  },
  listModels: async (): Promise<string[]> => {
    const res = await api.get('/models');
    return z.array(z.string()).parse(res.data?.models ?? []);
  },
};
