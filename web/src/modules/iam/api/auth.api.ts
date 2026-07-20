import { userSchema, type User } from '../model/auth';

import api from '@/services/client';

type RefreshResp = { access_token: string };
type SwitchTenantResp = { access_token: string; tenant_id: string };
type CreateTenantResp = { tenant_id: string };
type GuestLoginResp = { access_token: string; tenant_id: string; user: User };
type OAuthExchangeResp =
  | { kind: 'login'; access_token: string }
  | {
    kind: 'onboarding';
    onboarding_token: string;
    github_login: string;
    avatar_url: string;
  };

const withBearer = (token?: string) =>
  token ? { headers: { Authorization: `Bearer ${token}` }, _retry: true } as any : undefined;

export const authApi = {
  health: () => api.get('/health'),
  me: async (token?: string): Promise<User> => {
    const res = await api.get('/auth/me', withBearer(token));
    return userSchema.parse(res.data);
  },
  refresh: () => api.post<RefreshResp>('/auth/refresh').then((r) => r.data),
  exchangeOAuth: (code: string) =>
    api.post<OAuthExchangeResp>('/auth/oauth/exchange', { code }).then((r) => r.data),
  logout: () => api.post('/auth/logout'),
  guest: () =>
    api
      .post<GuestLoginResp>('/auth/guest')
      .then((r) => r.data),
  register: (payload: {
    onboarding_token: string;
    action: 'create' | 'join';
    tenant_name?: string;
    invitation_token?: string;
  }) =>
    api
      .post<{ access_token: string; tenant_id: string }>('/auth/register', payload)
      .then((r) => r.data),
  switchTenant: (tenantId: string) =>
    api
      .post<SwitchTenantResp>('/auth/switch-tenant', { tenant_id: tenantId })
      .then((r) => r.data),
  createUserTenant: (name: string) =>
    api
      .post<CreateTenantResp>('/auth/create-tenant', { tenant_name: name })
      .then((r) => r.data),
};
