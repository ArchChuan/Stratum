import { beforeEach, describe, expect, it, vi } from 'vitest';

import { auditApi } from './audit.api';

const client = vi.hoisted(() => ({
  get: vi.fn(),
}));
vi.mock('@/services/client', () => ({ default: client }));

const eventPayload = {
  id: 'evt-1',
  resource_kind: 'workflow',
  resource_id: 'wf-1',
  operation: 'create',
  actor_id: 'admin-1',
  actor_name: '管理员甲',
  created_at: '2026-08-09T11:00:00Z',
  before: null,
  after: { status: 'draft' },
};

describe('audit api', () => {
  beforeEach(() => {
    client.get.mockReset();
  });

  it('lists events with pagination through the shared client', async () => {
    client.get.mockResolvedValue({ data: { events: [eventPayload], total: 1 } });
    const page = await auditApi.listEvents({ page: 1, pageSize: 10 });
    expect(client.get).toHaveBeenCalledWith('/audit/events', { params: { page: 1, page_size: 10 } });
    expect(page.events).toHaveLength(1);
    expect(page.events[0].created_at).toBe('2026-08-09T11:00:00Z');
    expect(page.total).toBe(1);
  });

  it('passes resource_kind and actor_name filters plus RFC3339 time range', async () => {
    client.get.mockResolvedValue({ data: { events: [], total: 0 } });
    await auditApi.listEvents({
      from: '2026-08-09T00:00:00Z',
      to: '2026-08-09T23:59:59Z',
      resourceKind: 'workflow',
      actorName: '管理员甲',
      page: 2,
      pageSize: 50,
    });
    expect(client.get).toHaveBeenCalledWith('/audit/events', {
      params: {
        page: 2,
        page_size: 50,
        from: '2026-08-09T00:00:00Z',
        to: '2026-08-09T23:59:59Z',
        resource_kind: 'workflow',
        actor_name: '管理员甲',
      },
    });
  });

  it('omits empty filter fields', async () => {
    client.get.mockResolvedValue({ data: { events: [], total: 0 } });
    await auditApi.listEvents({ page: 1, pageSize: 20 });
    expect(client.get).toHaveBeenCalledWith('/audit/events', { params: { page: 1, page_size: 20 } });
  });

  it('fetches a single event by id, encoding the id', async () => {
    client.get.mockResolvedValue({ data: eventPayload });
    const event = await auditApi.getEvent('evt-1');
    expect(client.get).toHaveBeenCalledWith('/audit/events/evt-1');
    expect(event.resource_kind).toBe('workflow');
  });

  it('rejects a malformed event row', async () => {
    client.get.mockResolvedValue({ data: { events: [{ id: 'x' }], total: 1 } });
    await expect(auditApi.listEvents({ page: 1, pageSize: 20 })).rejects.toThrow();
  });
});
