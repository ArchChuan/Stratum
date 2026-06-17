import { z } from 'zod';

import { skillSchema, type Skill, type SkillFormValues } from '../model/skill';

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
  update: (id: string, data: SkillFormValues) => api.put(`/skills/${id}`, data),
  delete: (id: string) => api.delete(`/skills/${id}`),
  listModels: async (): Promise<string[]> => {
    const res = await api.get('/models');
    return z.array(z.string()).parse(res.data?.models ?? []);
  },
};
