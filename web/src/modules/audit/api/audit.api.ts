import { resourceChangeAuditSchema, resourceChangeAuditsPageSchema, type ResourceChangeAudit } from '../model/audit';

import api from '@/services/client';

export interface AuditFilter {
  from?: string;
  to?: string;
  resourceKind?: string;
  actorName?: string;
  page: number;
  pageSize: number;
}

// AuditFilter 为租户/平台两级审计共用筛选结构，snake_case 转换统一在此完成。
const buildAuditParams = (filter: AuditFilter): Record<string, string | number> => {
  const params: Record<string, string | number> = { page: filter.page, page_size: filter.pageSize };
  if (filter.from) params.from = filter.from;
  if (filter.to) params.to = filter.to;
  if (filter.resourceKind) params.resource_kind = filter.resourceKind;
  if (filter.actorName) params.actor_name = filter.actorName;
  return params;
};

export const auditApi = {
  listEvents: async (filter: AuditFilter): Promise<{ events: ResourceChangeAudit[]; total: number }> => {
    const response = await api.get('/audit/events', { params: buildAuditParams(filter) });
    return resourceChangeAuditsPageSchema.parse(response.data);
  },

  getEvent: async (id: string): Promise<ResourceChangeAudit> => {
    const response = await api.get(`/audit/events/${encodeURIComponent(id)}`);
    return resourceChangeAuditSchema.parse(response.data);
  },

  /** GET /admin/audit/platform/events — 平台级审计（租户/管理员/模型/厂商/平台参数变更）。 */
  listPlatformEvents: async (filter: AuditFilter): Promise<{ events: ResourceChangeAudit[]; total: number }> => {
    const response = await api.get('/admin/audit/platform/events', { params: buildAuditParams(filter) });
    return resourceChangeAuditsPageSchema.parse(response.data);
  },

  /** GET /admin/audit/platform/events/:id — 单条平台审计详情。 */
  getPlatformEvent: async (id: string): Promise<ResourceChangeAudit> => {
    const response = await api.get(`/admin/audit/platform/events/${encodeURIComponent(id)}`);
    return resourceChangeAuditSchema.parse(response.data);
  },
};
