import { beforeEach, describe, expect, it, vi } from 'vitest';

import { auditApi } from './audit.api';

const client = vi.hoisted(() => ({
  get: vi.fn(),
}));
vi.mock('@/services/client', () => ({ default: client }));

const eventPayload = {
  id: 'evt-1',
  tenant_id: 't1',
  actor: { actor_type: 'user', actor_id: 'admin-1' },
  action: 'POST /v1/agents',
  resource_type: 'http_request',
  resource_id: 'agent-1',
  before: null,
  after: { name: 'v2' },
  request_id: 'req-1',
  trace_id: 'trace-1',
  risk_level: 'medium',
  outcome: 'success',
  occurred_at: '2026-08-09T11:00:00Z',
};

describe('audit api', () => {
  beforeEach(() => {
    client.get.mockReset();
  });

  it('lists events with pagination through the shared client', async () => {
    client.get.mockResolvedValue({ data: { events: [eventPayload], count: 1, total: 1 } });
    const page = await auditApi.listEvents({ page: 1, pageSize: 10 });
    expect(client.get).toHaveBeenCalledWith('/audit/events', { params: { page: 1, page_size: 10 } });
    expect(page.events).toHaveLength(1);
    expect(page.events[0].occurred_at).toBe('2026-08-09T11:00:00Z');
    expect(page.total).toBe(1);
  });

  it('passes filters and RFC3339 time range to the query params', async () => {
    client.get.mockResolvedValue({ data: { events: [], count: 0, total: 0 } });
    await auditApi.listEvents({
      from: '2026-08-09T00:00:00Z',
      to: '2026-08-09T23:59:59Z',
      action: 'POST /v1/agents',
      risk_level: 'high',
      outcome: 'error',
      resource_type: 'http_request',
      page: 2,
      pageSize: 50,
    });
    expect(client.get).toHaveBeenCalledWith('/audit/events', {
      params: {
        page: 2,
        page_size: 50,
        from: '2026-08-09T00:00:00Z',
        to: '2026-08-09T23:59:59Z',
        action: 'POST /v1/agents',
        risk_level: 'high',
        outcome: 'error',
        resource_type: 'http_request',
      },
    });
  });

  it('omits empty filter fields', async () => {
    client.get.mockResolvedValue({ data: { events: [], count: 0, total: 0 } });
    await auditApi.listEvents({ page: 1, pageSize: 20 });
    expect(client.get).toHaveBeenCalledWith('/audit/events', { params: { page: 1, page_size: 20 } });
  });

  it('fetches a single event by id, encoding the id', async () => {
    client.get.mockResolvedValue({ data: eventPayload });
    const event = await auditApi.getEvent('evt-1');
    expect(client.get).toHaveBeenCalledWith('/audit/events/evt-1');
    expect(event.actor.actor_type).toBe('user');
  });

  it('rejects a malformed event row', async () => {
    client.get.mockResolvedValue({ data: { events: [{ id: 'x' }], total: 1 } });
    await expect(auditApi.listEvents({ page: 1, pageSize: 20 })).rejects.toThrow();
  });
});
