import { beforeEach, describe, expect, it, vi } from 'vitest';

import { evaluationApi } from './evaluation.api';

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/services/client', () => ({ default: client }));

describe('evaluation center api', () => {
  beforeEach(() => { client.get.mockReset(); client.post.mockReset(); });

  it.each([
    ['getOverview', '/evaluations/overview', undefined, { resources: 0, suites: 0, runs: 0, candidates: 0, experiments: 0 }],
    ['listResources', '/evaluations/resources', { resource_kind: 'agent', status: 'active', cursor: 'next', limit: 20 }, { items: [] }],
    ['listSuites', '/evaluations/suites', { resource_kind: 'skill' }, { items: [] }],
    ['listRuns', '/evaluations/runs', { resource_id: 'resource-1' }, { items: [] }],
    ['listCandidates', '/evaluations/candidates', { status: 'proposed' }, { items: [] }],
    ['listExperiments', '/evaluations/experiments', { status: 'active' }, { items: [] }],
  ] as const)('%s uses the shared client and query params', async (method, path, params, data) => {
    client.get.mockResolvedValue({ data });
    await (evaluationApi[method] as (filters?: unknown) => Promise<unknown>)(params);
    if (params) expect(client.get).toHaveBeenCalledWith(path, { params });
    else expect(client.get).toHaveBeenCalledWith(path);
  });

  it('encodes timeline resource paths and forwards cursors', async () => {
    client.get.mockResolvedValue({ data: { items: [] } });
    await evaluationApi.getTimeline('knowledge', 'space/name', { cursor: 'next', limit: 10 });
    expect(client.get).toHaveBeenCalledWith('/evaluations/resources/knowledge/space%2Fname/timeline', {
      params: { cursor: 'next', limit: 10 },
    });
  });

  it.each([
    ['rejectCandidate', '/evaluations/candidates/candidate-1/reject'],
    ['pauseExperiment', '/evaluations/experiments/candidate-1/pause'],
    ['promoteExperiment', '/evaluations/experiments/candidate-1/promote'],
    ['rollbackExperiment', '/evaluations/experiments/candidate-1/rollback'],
  ] as const)('%s omits actor identity', async (method, path) => {
    client.post.mockResolvedValue({ data: { id: 'candidate-1', status: 'paused' } });
    await (evaluationApi[method] as (id: string, command: unknown) => Promise<unknown>)('candidate-1', {
      reason: '人工复核', idempotency_key: 'request-1', expected_state_version: 2,
    });
    expect(client.post).toHaveBeenCalledWith(path, {
      reason: '人工复核', idempotency_key: 'request-1', expected_state_version: 2,
    });
    expect(client.post.mock.calls[0][1]).not.toHaveProperty('actor_id');
  });
});
