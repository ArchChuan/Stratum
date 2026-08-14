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

export const auditApi = {
  listEvents: async (filter: AuditFilter): Promise<{ events: ResourceChangeAudit[]; total: number }> => {
    const params: Record<string, string | number> = { page: filter.page, page_size: filter.pageSize };
    if (filter.from) params.from = filter.from;
    if (filter.to) params.to = filter.to;
    if (filter.resourceKind) params.resource_kind = filter.resourceKind;
    if (filter.actorName) params.actor_name = filter.actorName;
    const response = await api.get('/audit/events', { params });
    return resourceChangeAuditsPageSchema.parse(response.data);
  },

  getEvent: async (id: string): Promise<ResourceChangeAudit> => {
    const response = await api.get(`/audit/events/${encodeURIComponent(id)}`);
    return resourceChangeAuditSchema.parse(response.data);
  },
};
