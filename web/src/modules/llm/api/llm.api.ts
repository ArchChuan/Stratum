import type {
  CreateProviderInput,
  Model,
  Provider,
  UpdateModelInput,
  UpdateModelPolicyInput,
  UpdateProviderInput,
} from '../model/llm';

import api from '@/services/client';

interface ProviderListResponse {
  providers: Provider[];
}

interface ModelListResponse {
  models: Model[];
}

interface DiscoverResponse {
  models: Model[];
  count: number;
}

interface HealthResponse {
  status: string;
}

interface MessageResponse {
  message: string;
}

export const llmApi = {
  // Providers
  listProviders: async (): Promise<Provider[]> => {
    const res = await api.get<ProviderListResponse>('/admin/providers');
    return res.data.providers ?? [];
  },

  createProvider: async (data: CreateProviderInput): Promise<Provider> => {
    const res = await api.post<Provider>('/admin/providers', data);
    return res.data;
  },

  updateProvider: (id: string, data: UpdateProviderInput) =>
    api.put(`/admin/providers/${id}`, data),

  deleteProvider: async (id: string): Promise<void> => {
    await api.delete<MessageResponse>(`/admin/providers/${id}`);
  },

  discoverModels: async (id: string): Promise<DiscoverResponse> => {
    const res = await api.post<DiscoverResponse>(`/admin/providers/${id}/discover`);
    return res.data;
  },

  healthCheck: async (id: string): Promise<HealthResponse> => {
    const res = await api.post<HealthResponse>(`/admin/providers/${id}/health`);
    return res.data;
  },

  // Models
  listModels: async (params?: {
    capability?: string;
    providerId?: string;
  }): Promise<Model[]> => {
    const res = await api.get<ModelListResponse>('/admin/models', { params });
    return res.data.models ?? [];
  },

  getModel: async (id: string): Promise<Model> => {
    const res = await api.get<Model>(`/admin/models/${id}`);
    return res.data;
  },

  updateModel: (id: string, data: UpdateModelInput) =>
    api.put(`/admin/models/${id}`, data),

  updateModelPolicy: (id: string, data: UpdateModelPolicyInput) =>
    api.patch<Model>(`/admin/models/${id}/policy`, data),

  toggleModel: async (id: string, enabled: boolean): Promise<void> => {
    await api.patch<MessageResponse>(`/admin/models/${id}/toggle`, { enabled });
  },

  deleteModel: async (id: string): Promise<void> => {
    await api.delete<MessageResponse>(`/admin/models/${id}`);
  },
};
