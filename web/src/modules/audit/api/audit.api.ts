import { auditEventSchema, auditEventsPageSchema, type AuditEvent } from '../model/audit';

import api from '@/services/client';

export interface AuditEventFilter {
  from?: string;
  to?: string;
  action?: string;
  risk_level?: string;
  outcome?: string;
  resource_type?: string;
  page: number;
  pageSize: number;
}

export const auditApi = {
  listEvents: async (filter: AuditEventFilter): Promise<{ events: AuditEvent[]; total: number }> => {
    const params: Record<string, string | number> = { page: filter.page, page_size: filter.pageSize };
    if (filter.from) params.from = filter.from;
    if (filter.to) params.to = filter.to;
    if (filter.action) params.action = filter.action;
    if (filter.risk_level) params.risk_level = filter.risk_level;
    if (filter.outcome) params.outcome = filter.outcome;
    if (filter.resource_type) params.resource_type = filter.resource_type;
    const response = await api.get('/audit/events', { params });
    return auditEventsPageSchema.parse(response.data);
  },

  getEvent: async (id: string): Promise<AuditEvent> => {
    const response = await api.get(`/audit/events/${encodeURIComponent(id)}`);
    return auditEventSchema.parse(response.data);
  },
};
