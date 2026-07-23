import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

import {
  expect, request, test as base, type APIRequestContext, type Page,
} from '../../web/node_modules/@playwright/test/index.js';

type ResourceKind = 'skill' | 'agent' | 'mcp' | 'knowledge';
type ResourceFixture = {
  id: string;
  baselineRevision: string;
  candidateRevision: string;
  experimentId: string;
  recommendation: 'promote' | 'rollback';
  decision: 'promote' | 'rollback';
};
type Manifest = {
  tenantId: string;
  userId: string;
  foreignResourceId: string;
  resources: Record<ResourceKind, ResourceFixture>;
  failureScenarios: Array<{
    name: string;
    resourceKind: ResourceKind;
    resourceId: string;
    stableRevision: string;
  }>;
  ids: { memberDenied: string; duplicate: string };
  liveEvidence: {
    mcp: { serverId: string; revisionId: string; suiteRevisionId: string; jobId: string; runId: string;
      toolCalls: number; encryptedPayloadVerified: boolean };
    agent: { resourceId: string; revisionId: string; suiteRevisionId: string; jobId: string; runId: string;
      traceId: string; tokens: number; toolTraces: number; traceEvents: number; failureJobId: string;
      failureRunId: string; failureError: string; recoveryJobId: string; recoveryRunId: string };
    skill: { resourceId: string; revisionId: string; suiteRevisionId: string; jobId: string; runId: string;
      traceId: string; tokens: number; llmRequests: number };
    knowledge: { resourceId: string; workspaceId: string; revisionId: string; documentId: string;
      chunkIndex: number; jobId: string; runId: string; failureJobId: string; failureRunId: string;
      failureError: string; recoveryJobId: string; recoveryRunId: string };
  };
};

const required = (name: string): string => {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for evaluation evolution E2E`);
  return value;
};

const manifest = (): Manifest => JSON.parse(readFileSync(required('E2E_FIXTURE_MANIFEST'), 'utf8')) as Manifest;

const psql = (sql: string): string => execFileSync('docker', ['exec', '-i', required('E2E_POSTGRES_CONTAINER'),
  'psql', '-U', 'stratum_e2e', '-d', 'stratum_e2e', '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', sql], {
  encoding: 'utf8',
  env: process.env,
  stdio: ['ignore', 'pipe', 'pipe'],
}).trim();

const literal = (value: string): string => `'${value.replaceAll("'", "''")}'`;
const identifier = (value: string): string => `"${value.replaceAll('"', '""')}"`;
const tenantSchema = (tenantId: string): string => identifier(`tenant_${tenantId}`);

const forbiddenPatterns = [
  /Bearer\s+[A-Za-z0-9._~+\/-]+=*/i,
  /(?:api[_-]?key|access[_-]?token|credential|secret)\s*[=:]\s*["']?[^\s,"'}]+/i,
  /raw[_ -]?payload/i,
  /upstream[_ -]?(?:body|response)/i,
];

type Fixtures = {
  manifest: Manifest;
  adminApi: APIRequestContext;
  memberApi: APIRequestContext;
  authenticatedPage: Page;
  scanSafe: (value: string) => Promise<void>;
  db: {
    experiment: (id: string) => Promise<{ status: string; recommendation: string; stateVersion: number }>;
    deployment: (kind: ResourceKind, id: string) => Promise<{ stableRevision: string }>;
    evidenceProjection: () => Promise<string>;
    promotionEvidence: () => Promise<Array<{ eligible: boolean }>>;
  };
};

export const test = base.extend<Fixtures>({
  manifest: async ({}, use) => { await use(manifest()); },
  adminApi: async ({}, use) => {
    const context = await request.newContext({ baseURL: required('E2E_API_URL'),
      extraHTTPHeaders: { Authorization: `Bearer ${required('E2E_ADMIN_TOKEN')}` } });
    await use(context);
    await context.dispose();
  },
  memberApi: async ({}, use) => {
    const context = await request.newContext({ baseURL: required('E2E_API_URL'),
      extraHTTPHeaders: { Authorization: `Bearer ${required('E2E_MEMBER_TOKEN')}` } });
    await use(context);
    await context.dispose();
  },
  authenticatedPage: async ({ page }, use) => {
    const current = manifest();
    await page.route('**/auth/refresh', async (route) => route.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify({ access_token: required('E2E_ADMIN_TOKEN') }) }));
    await page.route('**/auth/me', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      sub: current.userId, tenant_id: current.tenantId, role: 'admin', system_role: 'user', global_role: 'user',
      memberships: [{ tenant_id: current.tenantId, role: 'admin' }],
    }) }));
    await use(page);
  },
  scanSafe: async ({}, use) => {
    await use(async (value: string) => {
      for (const pattern of forbiddenPatterns) expect(value, `sensitive marker matched ${pattern}`).not.toMatch(pattern);
      const knownSecret = process.env.E2E_KNOWN_SECRET;
      if (knownSecret) expect(value).not.toContain(knownSecret);
    });
  },
  db: async ({ manifest: current }, use) => {
    const schema = tenantSchema(current.tenantId);
    await use({
      experiment: async (id) => {
        const [status, recommendation, version] = psql(`SELECT status,recommendation,state_version FROM ${schema}.evaluation_experiments WHERE id=${literal(id)}`).split('|');
        return { status, recommendation, stateVersion: Number(version) };
      },
      deployment: async (kind, id) => ({ stableRevision: psql(
        `SELECT stable_revision_id FROM ${schema}.evaluation_deployments WHERE resource_kind=${literal(kind)} AND resource_id=${literal(id)}`,
      ) }),
      evidenceProjection: async () => psql(`SELECT jsonb_build_object('decision_count',count(*),'actions',array_agg(action ORDER BY created_at)) FROM ${schema}.experiment_decisions`),
      promotionEvidence: async () => JSON.parse(psql(`SELECT COALESCE(jsonb_agg(jsonb_build_object('eligible', recommendation='promote' AND NOT safety_stopped)),'[]'::jsonb) FROM ${schema}.evaluation_experiments`)) as Array<{ eligible: boolean }>,
    });
  },
});

export { expect };
