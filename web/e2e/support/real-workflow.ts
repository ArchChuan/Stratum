import { expect, request, type BrowserContext } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repositoryRoot = fileURLToPath(new URL('../../..', import.meta.url));
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export interface RealSession {
  tenantId: string;
  userId: string;
}

export interface PublishedWorkflow {
  definitionId: string;
  versionId: string;
  name: string;
}

const runSQL = (sql: string) => execFileSync(
  'docker',
  ['compose', 'exec', '-T', 'postgres', 'psql', '-U', 'stratum', '-d', 'stratum', '-qAt', '-c', sql],
  { cwd: repositoryRoot, encoding: 'utf8' },
).trim();

const requireUUID = (value: string, field: string) => {
  expect(value, `${field} must be a UUID`).toMatch(uuidPattern);
  return value;
};

export const createRealSession = async (context: BrowserContext, role: 'admin' | 'member'): Promise<RealSession> => {
  const response = await context.request.post('http://localhost:8080/auth/guest');
  expect(response.status()).toBe(201);
  const body = await response.json() as { tenant_id: string; user: { sub: string } };
  const tenantId = requireUUID(body.tenant_id, 'tenant_id');
  const userId = requireUUID(body.user.sub, 'user_id');
  if (role === 'admin') {
    const updated = runSQL(
      `UPDATE public.tenant_members SET role='admin' WHERE tenant_id='${tenantId}' AND user_id='${userId}' RETURNING 1`,
    );
    expect(updated).toContain('1');
  }
  return { tenantId, userId };
};

export const createPublishedApprovalWorkflow = async (): Promise<PublishedWorkflow> => {
  const api = await request.newContext({ baseURL: 'http://localhost:8080' });
  try {
    const guestResponse = await api.post('/auth/guest');
    expect(guestResponse.status()).toBe(201);
    const guest = await guestResponse.json() as { tenant_id: string; user: { sub: string } };
    const tenantId = requireUUID(guest.tenant_id, 'tenant_id');
    const userId = requireUUID(guest.user.sub, 'user_id');
    expect(runSQL(
      `UPDATE public.tenant_members SET role='admin' WHERE tenant_id='${tenantId}' AND user_id='${userId}' RETURNING 1`,
    )).toContain('1');
    const refreshResponse = await api.post('/auth/refresh');
    expect(refreshResponse.status()).toBe(200);
    const { access_token: accessToken } = await refreshResponse.json() as { access_token: string };
    const name = `E2E-审批流程-${Date.now()}`;
    const createResponse = await api.post('/workflows', {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        name,
        description: '真实浏览器运行验收',
        spec: {
          nodes: [{
            id: 'approval-1', name: '管理员审批', type: 'approval', agent_id: '', input_mapping: {},
            output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
          }],
          edges: [],
          max_concurrency: 0,
        },
        input_schema: { task_label: '审批事项', task_description: '请输入待审批事项', fields: [] },
      },
    });
    expect(createResponse.status()).toBe(201);
    const definition = await createResponse.json() as { id: string };
    const publishResponse = await api.post(`/workflows/${definition.id}/publish`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    expect(publishResponse.status()).toBe(201);
    const version = await publishResponse.json() as { id: string };
    return { definitionId: definition.id, versionId: version.id, name };
  } finally {
    await api.dispose();
  }
};

export const queryTenant = (tenantId: string, sql: string) => {
  requireUUID(tenantId, 'tenant_id');
  return runSQL(`SET search_path TO "tenant_${tenantId}", public; ${sql}`);
};
