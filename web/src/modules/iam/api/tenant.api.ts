import { z } from 'zod';

import {
  memberSchema,
  tenantSettingsSchema,
  tenantSummarySchema,
  adminTenantSchema,
  type TenantSettings,
  type TenantSummary,
} from '../model/auth';

import { ADMIN_USER_SEARCH_LIMIT } from '@/constants';
import api from '@/services/client';

const withBearer = (token?: string) =>
  token ? { headers: { Authorization: `Bearer ${token}` }, _retry: true } as any : undefined;

const memberPageSchema = z.object({
  members: z.array(memberSchema),
  total: z.number(),
  page: z.number(),
  page_size: z.number(),
});

const adminTenantPageSchema = z.object({
  tenants: z.array(adminTenantSchema),
  total: z.number(),
  page: z.number(),
  page_size: z.number(),
});

const invitationSchema = z.object({
  invitation_code: z.string().min(1),
  email: z.string().email(),
  role: z.enum(['admin', 'member']),
});

export type MemberPage = z.infer<typeof memberPageSchema>;

// 平台管理员（users.global_role）行，平台管理页使用。avatar_url 后端为 *string，
// 无头像时输出 null，这里归一化为空字符串。
export const adminUserSchema = z
  .object({
    user_id: z.string(),
    username: z.string().optional().default(''),
    github_login: z.string().optional().default(''),
    avatar_url: z.string().nullish().transform((v) => v ?? ''),
    global_role: z.string().optional().default(''),
  })
  .passthrough();
export type AdminUser = z.infer<typeof adminUserSchema>;

export type CreateAdminTenantInput = {
  name: string;
  slug: string;
  plan: 'free' | 'pro' | 'enterprise';
  status: 'active' | 'suspended';
};

export const tenantApi = {
  listMine: async (token?: string): Promise<TenantSummary[]> => {
    const res = await api.get('/tenant/list', withBearer(token));
    return z.array(tenantSummarySchema).parse(res.data?.tenants ?? []);
  },
  settings: async (token?: string): Promise<TenantSettings> => {
    const res = await api.get('/tenant/settings', withBearer(token));
    const data = res.data ?? {};
    return tenantSettingsSchema.parse({
      tenant_id: data.tenant_id,
      tenant_name: data.tenant_name,
      is_default: data.is_default,
      settings: data.settings ?? {},
    });
  },
  updateSettings: (patch: Record<string, unknown>) => api.patch('/tenant/settings', patch),
  members: async (page: number, pageSize: number, role?: string): Promise<MemberPage> => {
    const res = await api.get('/tenant/members', {
      params: { page, page_size: pageSize, ...(role ? { role } : {}) },
    });
    return memberPageSchema.parse(res.data);
  },
  inviteMember: async (data: { email: string; role: string }) => {
    const res = await api.post('/tenant/members/invite', data);
    return invitationSchema.parse(res.data);
  },
  updateMemberRole: (userId: string, role: string) =>
    api.patch(`/tenant/members/${userId}/role`, { role }),
  removeMember: (userId: string) => api.delete(`/tenant/members/${userId}`),
  joinTenant: (onboardingToken: string, inviteCode: string) =>
    api.post('/auth/register', {
      onboarding_token: onboardingToken,
      action: 'join',
      invitation_token: inviteCode,
    }),
  joinExisting: (inviteCode: string) =>
    api.post<{ tenant_id: string }>('/tenant/join', { invitation_code: inviteCode }).then((res) => res.data),
  // admin
  listAllTenants: async (page: number, pageSize: number) => {
    const res = await api.get('/admin/tenants', { params: { page, page_size: pageSize } });
    return adminTenantPageSchema.parse(res.data);
  },
  setTenantEnabled: (tenantId: string, enabled: boolean) =>
    api.patch(`/admin/tenants/${tenantId}`, {
      status: enabled ? 'active' : 'suspended',
    }),
  createTenant: (data: CreateAdminTenantInput) => api.post('/admin/tenants', data),
  adminDeleteTenant: (tenantId: string) => api.delete(`/admin/tenants/${tenantId}`),
  deleteSelf: () => api.delete('/tenant'),
  // 平台管理员管理（users.global_role）。前端按角色置灰写控件；写操作由后端 RequireGlobalAdmin 守卫（fail-closed）。
  searchAdminCandidates: async (query: string, limit = ADMIN_USER_SEARCH_LIMIT): Promise<AdminUser[]> => {
    const res = await api.get('/admin/users', { params: { query, limit } });
    return z.object({ users: z.array(adminUserSchema) }).parse(res.data).users;
  },
  listAdmins: async (): Promise<AdminUser[]> => {
    const res = await api.get('/admin/admins');
    return z.object({ admins: z.array(adminUserSchema) }).parse(res.data).admins;
  },
  setAdminRole: (userId: string) => api.post('/admin/admins', { user_id: userId }),
  removeAdminRole: (userId: string) => api.delete(`/admin/admins/${userId}`),
};
