import { describe, expect, it } from 'vitest';

import {
  candidatePageSchema,
  errorResponseSchema,
  evaluationJobSchema,
  experimentPageSchema,
  optimizationResponseSchema,
  resourcePageSchema,
  resourceRefSchema,
  timelinePageSchema,
  safeSummarySchema,
  candidateCommandResponseSchema,
  experimentCommandResponseSchema,
} from './evaluation';

describe('evaluation model', () => {
  it('parses completed job with result id', () => {
    const job = evaluationJobSchema.parse({ job_id: 'job-1', status: 'succeeded', result_id: 'run-1' });
    expect(job.result_id).toBe('run-1');
  });

  it('parses generated candidate revisions', () => {
    const response = optimizationResponseSchema.parse({
      job: { id: 'optimization-1', status: 'succeeded' },
      candidates: [
        {
          id: 'candidate-record-1',
          optimization_job_id: 'optimization-1',
          revision: { kind: 'skill', resource_id: 'skill-1', revision_id: 'candidate-1' },
          parent_revision_id: 'version-1',
          source: 'parameter_search',
        },
      ],
    });
    expect(response.candidates[0].revision.revision_id).toBe('candidate-1');
  });

  it.each(['skill', 'agent', 'mcp', 'knowledge'] as const)('supports %s resources', (kind) => {
    expect(resourceRefSchema.parse({ kind, resource_id: 'resource-1', revision_id: 'revision-1' }).kind).toBe(kind);
  });

  it('parses safe center summaries and rejects raw candidate payloads', () => {
    const resources = resourcePageSchema.parse({ items: [{
      id: 'resource-1', resource_id: 'skill-1', resource_kind: 'skill', status: 'active',
      safe_summary: { resource_name: '问答技能', changed_fields: ['instructions'] }, created_at: '2026-01-01T00:00:00Z',
    }] });
    const candidate = {
      id: 'candidate-1', resource_id: 'skill-1', revision_id: 'revision-2', parent_revision_id: 'revision-1',
      source: 'optimization', status: 'proposed', resource_kind: 'skill', state_version: 1,
      safe_diff: {
        changed_fields: ['instructions'],
        changes: { instructions: { before: '旧指令', after: '新指令' } },
        parent_missing: false,
      },
      created_at: '2026-01-01T00:00:00Z',
    };
    expect(resources.items[0].safe_summary.resource_name).toBe('问答技能');
    expect(candidatePageSchema.parse({ items: [candidate] }).items[0].safe_diff.changed_fields).toEqual(['instructions']);
    expect(() => candidatePageSchema.parse({ items: [{ ...candidate, payload: { prompt: 'secret' } }] })).toThrow();
  });

  it('parses experiment gates and timeline events', () => {
    const experiments = experimentPageSchema.parse({ items: [{
      id: 'experiment-1', resource_id: 'agent-1', stable_revision_id: 'stable-1', canary_revision_id: 'canary-1',
      status: 'active', recommendation: 'hold', resource_kind: 'agent', stage_percent: 5, safety_stopped: false,
      state_version: 2,
      gates: { quality: 'passed', cost: 'pending', latency: 'passed', error_rate: 'passed', security: 'passed' },
      created_at: '2026-01-01T00:00:00Z',
    }] });
    const timeline = timelinePageSchema.parse({ items: [{
      id: 'event-1', kind: 'run', status: 'succeeded', summary: '评测通过', resource_id: 'agent-1',
      resource_kind: 'agent', created_at: '2026-01-01T00:00:00Z',
    }] });
    expect(experiments.items[0].gates?.security).toBe('passed');
    expect(timeline.items[0].summary).toBe('评测通过');
  });

  it('keeps the frozen error envelope', () => {
    expect(errorResponseSchema.parse({ error: '操作失败' })).toEqual({ error: '操作失败' });
    expect(() => errorResponseSchema.parse({ message: '操作失败' })).toThrow();
  });

  it.each([
    ['skill', { label: '客服技能', nested: { enabled: true, count: 2 } }],
    ['agent', { model_name: 'qwen-plus', tools: ['search', 'calculator'] }],
    ['mcp', { transport: 'stdio', capabilities: { tools: 3, resources: 1 } }],
    ['knowledge', { workspace_name: '产品手册', chunking: { strategy: 'semantic' } }],
  ])('accepts JSON-safe %s adapter summaries with legitimate extension keys', (_kind, summary) => {
    expect(safeSummarySchema.parse(summary)).toEqual(summary);
  });

  it.each([
    { payload: { instructions: 'raw' } },
    { nested: { raw_prompt: 'secret' } },
    { auth: { credentials: { username: 'u' } } },
    { api_key: 'secret' },
    { nested: [{ token: 'secret' }] },
    { retrieved_content: 'document body' },
    { document_content: 'document body' },
    { tool: { arguments: { query: 'private' } } },
    { tool_raw_response: 'private' },
    { encrypted_payload_ref: 'object://secret' },
  ])('rejects recursively sensitive or raw summary keys', (summary) => {
    expect(() => safeSummarySchema.parse(summary)).toThrow();
  });

  it('strictly parses candidate and experiment command responses', () => {
    expect(candidateCommandResponseSchema.parse({
      id: 'candidate-1', resource_id: 'skill-1', revision_id: 'revision-2', parent_revision_id: 'revision-1',
      source: 'optimization', status: 'rejected', resource_kind: 'skill', state_version: 2, safe_diff: {
        changed_fields: ['label'], changes: { label: { before: 'old', after: 'new' } }, parent_missing: false,
      }, created_at: '2026-01-01T00:00:00Z',
    }).state_version).toBe(2);
    expect(experimentCommandResponseSchema.parse({
      id: 'experiment-1', resource_kind: 'agent', resource_id: 'agent-1', stable_revision_id: 'stable-1',
      canary_revision_id: 'canary-1', suite_revision_id: 'suite-1', status: 'paused', stage: 5,
      policy: { stages: [5, 20], min_samples: 100, min_observation_minutes: 60, max_cost_regression: 0.1,
        max_latency_regression: 0.2, max_error_rate_increase: 0.01 }, state_version: 3,
      recommendation: 'hold', safety_stopped: false,
    }).state_version).toBe(3);
  });
});
