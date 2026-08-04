import {
  collaborationDetailSchema,
  collaborationListSchema,
  collaborationSchema,
  type Collaboration,
  type CollaborationDetail,
} from '../model/collaboration';

import api from '@/services/client';

export interface CreateCollabPayload {
  task_description: string;
  strategy: string;
  participants: string[];
}

export const collaborationApi = {
  list: async (limit = 50, offset = 0): Promise<Collaboration[]> => {
    const response = await api.get('/collaborations', { params: { limit, offset } });
    return collaborationListSchema.parse(response.data).collaborations;
  },
  create: async (payload: CreateCollabPayload): Promise<Collaboration> => {
    const response = await api.post('/collaborations', payload);
    return collaborationSchema.parse(response.data);
  },
  get: async (id: string): Promise<CollaborationDetail> => {
    const response = await api.get(`/collaborations/${id}`);
    return collaborationDetailSchema.parse(response.data);
  },
  start: async (id: string): Promise<Collaboration> => {
    const response = await api.post(`/collaborations/${id}/start`);
    return collaborationSchema.parse(response.data);
  },
  cancel: async (id: string) => {
    await api.post(`/collaborations/${id}/cancel`);
  },
};
