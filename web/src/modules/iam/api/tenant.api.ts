import { z } from 'zod';

import {
  memberSchema,
  tenantSettingsSchema,
  tenantSummarySchema,
  adminTenantSchema,
  type TenantSettings,
  type TenantSummary,
  type AdminTenant,
} from '../model/auth';

import api from '@/services/client';

const withBearer = (token?: string) =>
  token ? { headers: { Authorization: `Bearer ${token}` }, _retry: true } as any : undefined;

const memberPageSchema = z.object({
  members: z.array(memberSchema),
  total: z.number(),
  page: z.number(),
  page_size: z.number(),
});

export type MemberPage = z.infer<typeof memberPageSchema>;

export const tenantApi = {
  listMine: async (token?: string): Promise<TenantSummary[]> => {
    const res = await api.get('/tenant/list', withBearer(token));
    return z.array(tenantSummarySchema).parse(res.data?.tenants ?? []);
  },
  settings: async (token?: string): Promise<TenantSettings> => {
    const res = await api.get('/tenant/settings', withBearer(token));
    const data = res.data ?? {};
    const inner = (data.settings ?? {}) as Record<string, unknown>;
    return tenantSettingsSchema.parse({
      tenant_id: data.tenant_id,
      tenant_name: data.tenant_name,
      embed_model: inner.embed_model,
      llm_api_keys: inner.llm_api_keys,
    });
  },
  updateSettings: (patch: Record<string, unknown>) => api.patch('/tenant/settings', patch),
  setEmbedModel: (embedModel: string) =>
    api.patch('/tenant/embed-model', { embed_model: embedModel }),
  members: async (page: number, pageSize: number): Promise<MemberPage> => {
    const res = await api.get('/tenant/members', { params: { page, page_size: pageSize } });
    return memberPageSchema.parse(res.data);
  },
  inviteMember: (data: { email: string; role: string }) =>
    api.post('/tenant/members/invite', data),
  updateMemberRole: (userId: string, role: string) =>
    api.patch(`/tenant/members/${userId}/role`, { role }),
  removeMember: (userId: string) => api.delete(`/tenant/members/${userId}`),
  joinTenant: (onboardingToken: string, inviteCode: string) =>
    api.post('/auth/register', {
      onboarding_token: onboardingToken,
      action: 'join',
      invitation_token: inviteCode,
    }),
  // admin
  listAllTenants: async (): Promise<AdminTenant[]> => {
    const res = await api.get('/admin/tenants');
    return z.array(adminTenantSchema).parse(res.data?.tenants ?? res.data ?? []);
  },
  setTenantEnabled: (tenantId: string, enabled: boolean) =>
    api.patch(`/admin/tenants/${tenantId}`, {
      status: enabled ? 'active' : 'suspended',
    }),
  createTenant: (data: { name: string }) => api.post('/admin/tenants', data),
  adminDeleteTenant: (tenantId: string) => api.delete(`/admin/tenants/${tenantId}`),
  deleteSelf: () => api.delete('/tenant'),
};
