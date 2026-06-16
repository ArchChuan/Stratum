import api from './client';

export const createTenant = (data) => api.post('/admin/tenants', data);
export const joinTenant = (onboardingToken, inviteCode) =>
  api.post('/auth/register', { onboarding_token: onboardingToken, action: 'join', invitation_token: inviteCode });
export const getTenantMembers = () => api.get('/tenant/members');
export const inviteMember = (data) => api.post('/tenant/members/invite', data);
export const updateMemberRole = (userId, role) => api.patch(`/tenant/members/${userId}/role`, { role });
export const removeMember = (userId) => api.delete(`/tenant/members/${userId}`);
export const getTenantSettings = () => api.get('/tenant/settings');
export const updateTenant = (data) => api.patch('/tenant/settings', data);
export const getAllTenants = () => api.get('/admin/tenants');
export const setTenantEnabled = (tenantId, enabled) => api.patch(`/admin/tenants/${tenantId}`, { status: enabled ? 'active' : 'suspended' });
export const getTenantList = () => api.get('/tenant/list');
export const switchTenant = (tenantId) => api.post('/auth/switch-tenant', { tenant_id: tenantId });
export const createUserTenant = (name) => api.post('/auth/create-tenant', { tenant_name: name });
export const setTenantEmbedModel = (embedModel) => api.patch('/tenant/embed-model', { embed_model: embedModel });
