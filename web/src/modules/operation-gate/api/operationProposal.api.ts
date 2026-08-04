import {
  operationProposalListSchema,
  operationProposalSchema,
  selfModifyResultSchema,
  type OperationProposal,
} from '../model/operationProposal';

import api from '@/services/client';

export const operationProposalApi = {
  listPending: async (): Promise<OperationProposal[]> => {
    const response = await api.get('/operation-proposals');
    return operationProposalListSchema.parse(response.data).proposals;
  },
  get: async (id: string) => {
    const response = await api.get(`/operation-proposals/${id}`);
    return operationProposalSchema.parse(response.data);
  },
  startReview: async (id: string) => {
    await api.post(`/operation-proposals/${id}/review`);
  },
  approve: async (id: string) => {
    await api.post(`/operation-proposals/${id}/approve`);
  },
  reject: async (id: string, note: string) => {
    await api.post(`/operation-proposals/${id}/reject`, { note });
  },
  selfModify: async (agentId: string, payload: Record<string, unknown>) => {
    const response = await api.post(`/agents/${agentId}/self-modify`, payload);
    return selfModifyResultSchema.parse(response.data);
  },
};
