import { expect, type BrowserContext } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const fixedIDPattern = /^[a-z0-9][a-z0-9_-]{2,127}$/i;
const apiURL = process.env.E2E_API_URL || 'http://127.0.0.1:8080';

export interface PlatformAssistantSession {
  tenantId: string;
  userId: string;
  accessToken: string;
}

const postgresContainer = () => {
  const value = process.env.E2E_POSTGRES_CONTAINER || '';
  expect(value, 'E2E_POSTGRES_CONTAINER is required').toMatch(/^[a-f0-9]{12,64}$/i);
  return value;
};

export const requireUUID = (value: string, field: string) => {
  expect(value, `${field} must be a UUID`).toMatch(uuidPattern);
  return value;
};

export const requireResourceID = (value: string, field: string) => {
  expect(
    uuidPattern.test(value) || fixedIDPattern.test(value),
    `${field} must be a UUID or fixed resource ID`,
  ).toBe(true);
  return value;
};

const runSQL = (sql: string) => execFileSync(
  'docker',
  ['exec', '-i', postgresContainer(), 'psql', '-U', 'stratum', '-d', 'stratum', '-qAt', '-c', sql],
  { encoding: 'utf8' },
).trim();

export const queryTenant = (tenantId: string, sql: string) => {
  requireUUID(tenantId, 'tenant_id');
  return runSQL(`SET search_path TO "tenant_${tenantId}", public; ${sql}`);
};

export const createPlatformAssistantSession = async (
  context: BrowserContext,
  role: 'admin' | 'member',
): Promise<PlatformAssistantSession> => {
  const guestResponse = await context.request.post(`${apiURL}/auth/guest`);
  expect(guestResponse.status()).toBe(201);
  const body = await guestResponse.json() as { tenant_id: string; user: { sub: string } };
  const tenantId = requireUUID(body.tenant_id, 'tenant_id');
  const userId = requireUUID(body.user.sub, 'user_id');
  if (role === 'admin') {
    const updated = runSQL(
      `UPDATE public.tenant_members SET role='admin' WHERE tenant_id='${tenantId}' AND user_id='${userId}' RETURNING 1`,
    );
    expect(updated).toContain('1');
  }

  const refreshResponse = await context.request.post(`${apiURL}/auth/refresh`);
  expect(refreshResponse.status()).toBe(200);
  const refresh = await refreshResponse.json() as { access_token: string };
  expect(refresh.access_token).not.toBe('');

  if (role === 'admin') {
    const headers = { Authorization: `Bearer ${refresh.access_token}` };
    const providerResponse = await context.request.patch(`${apiURL}/tenant/settings`, {
      headers,
      data: { settings: { llm_api_keys: { qwen: 'platform-assistant-browser-e2e-key' } } },
    });
    expect(providerResponse.status()).toBe(200);
    const modelResponse = await context.request.put(`${apiURL}/agents/system/settings`, {
      headers,
      data: { llmModel: 'qwen-plus' },
    });
    expect(modelResponse.status()).toBe(200);
  }
  return { tenantId, userId, accessToken: refresh.access_token };
};

export const getProposalAsSession = async (
  context: BrowserContext,
  session: PlatformAssistantSession,
  proposalId: string,
) => {
  requireUUID(proposalId, 'proposal_id');
  return context.request.get(`${apiURL}/resource-change-proposals/${proposalId}`, {
    headers: { Authorization: `Bearer ${session.accessToken}` },
  });
};

export const proposalDatabaseEvidence = (tenantId: string, proposalId: string) => {
  requireUUID(proposalId, 'proposal_id');
  return queryTenant(tenantId, `
    SELECT status || '|' || edit_count || '|' || COALESCE(result->>'resourceId','')
    FROM resource_change_proposals WHERE id='${proposalId}'
  `);
};

export const proposalApplyEventEvidence = (tenantId: string, proposalId: string) => {
  requireUUID(proposalId, 'proposal_id');
  return queryTenant(tenantId, `
    SELECT count(*) FILTER (WHERE to_status='applying') || '|' ||
           count(*) FILTER (WHERE to_status='applied')
    FROM resource_change_proposal_events WHERE proposal_id='${proposalId}'
  `);
};

const terminalStatuses = new Set(['stale', 'expired', 'failed', 'unknown_outcome']);

export const seedTerminalProposal = (
  session: PlatformAssistantSession,
  status: 'stale' | 'expired' | 'failed' | 'unknown_outcome',
) => {
  expect(terminalStatuses.has(status)).toBe(true);
  const proposalId = randomUUID();
  const expiresAt = status === 'expired' ? "NOW() - INTERVAL '1 minute'" : "NOW() + INTERVAL '1 hour'";
  queryTenant(session.tenantId, `
    INSERT INTO resource_change_proposals
      (id, proposer_id, confirmer_id, resource_kind, resource_id, operation,
       baseline_fingerprint, baseline_projection, payload, safe_summary, status,
       result, error_code, created_at, updated_at, expires_at, edit_count)
    VALUES
      ('${proposalId}', '${session.userId}', '${session.userId}', 'knowledge_workspace', '', 'create',
       '', '{}'::jsonb,
       '{"name":"E2E Terminal ${status}","description":"terminal evidence","embeddingModel":"text-embedding-v3"}'::jsonb,
       '{"text":"terminal ${status}"}'::jsonb, '${status}', '{}'::jsonb,
       'proposal_${status}', NOW(), NOW(), ${expiresAt}, 0);
    INSERT INTO resource_change_proposal_events
      (id, proposal_id, actor_id, from_status, to_status, detail, created_at)
    VALUES
      (public.gen_uuid_v7(), '${proposalId}', '${session.userId}', 'applying', '${status}', '{}'::jsonb, NOW());
  `);
  return proposalId;
};

export const workspaceDatabaseEvidence = (tenantId: string, workspaceId: string) => {
  requireUUID(workspaceId, 'workspace_id');
  return queryTenant(tenantId, `
    SELECT name || '|' || COALESCE(description,'')
    FROM rag_workspaces WHERE id='${workspaceId}'
  `);
};
