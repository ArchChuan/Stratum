import { resourceChangeProposalSchema, type ProposalPayload, type ResourceChangeProposal } from '../model/proposal';

import api from '@/services/client';

export const proposalApi = {
  get: async (id: string): Promise<ResourceChangeProposal> => {
    const response = await api.get(`/resource-change-proposals/${id}`);
    return resourceChangeProposalSchema.parse(response.data);
  },
  update: async (id: string, payload: ProposalPayload): Promise<ResourceChangeProposal> => {
    const response = await api.patch(`/resource-change-proposals/${id}`, { payload });
    return resourceChangeProposalSchema.parse(response.data);
  },
  cancel: async (id: string): Promise<void> => {
    await api.post(`/resource-change-proposals/${id}/cancel`);
  },
  confirm: async (id: string): Promise<ResourceChangeProposal> => {
    const response = await api.post(`/resource-change-proposals/${id}/confirm`);
    return resourceChangeProposalSchema.parse(response.data);
  },
};
