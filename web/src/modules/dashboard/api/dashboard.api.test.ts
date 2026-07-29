import { beforeEach, describe, expect, it, vi } from 'vitest';

import { dashboardApi } from './dashboard.api';

const client = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/services/client', () => ({ default: client }));

describe('dashboard api', () => {
  beforeEach(() => client.get.mockReset());

  it('loads and parses the tenant overview through the shared client', async () => {
    const data = { agents: 1, skills: 2, knowledge_workspaces: 3, mcp_servers: 4,
      model_providers: 5, tenant_members: 6, workflows: 7, agent_user_messages_7d: 8 };
    client.get.mockResolvedValue({ data });

    await expect(dashboardApi.overview()).resolves.toEqual(data);
    expect(client.get).toHaveBeenCalledWith('/dashboard/overview');
  });

  it('rejects malformed overview data', async () => {
    client.get.mockResolvedValue({ data: { agents: '1' } });
    await expect(dashboardApi.overview()).rejects.toBeDefined();
  });
});
