import { describe, expect, it } from 'vitest';

import {
  candidatePageSchema,
  dimensionScoreSchema,
  errorResponseSchema,
  evaluationCaseSchema,
  evaluationJobSchema,
  evaluationRunSchema,
  experimentPageSchema,
  optimizationResponseSchema,
  observedTraceEvidenceSchema,
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
      promotion_evidence: { eligible: false,
        gates: { quality: 'passed', cost: 'pending', latency: 'passed', error_rate: 'passed', security: 'passed' },
        blockers: [{ code: 'insufficient_samples', category: 'sample', message: '样本量不足' }] },
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
    { auth: { cookie: 'session=secret' } },
    { auth: { Session: 'secret' } },
    { database: { connectionString: 'postgres://secret' } },
    { tls: { CERT: 'secret' } },
    { tls: { KEY: 'secret' } },
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

  it.each(['system_prompt', 'systemPrompt', 'developer-prompt', 'API_TOKEN', 'bearerToken', 'retrieved_chunks']) (
    'rejects unsafe alias %s while allowing safe metadata names', (key) => {
      expect(() => safeSummarySchema.parse({ nested: { [key]: 'raw' } })).toThrow();
      expect(safeSummarySchema.parse({ promptVersion: 'v2', token_count: 12, prompt_hash: 'sha256',
        model_token_limit: 8192 })).toBeTruthy();
    },
  );

  it.each([
    { changed_fields: Array.from({ length: 33 }, (_, index) => `field_${index}`), changes: {}, parent_missing: false },
    { changed_fields: ['label', 'label'], changes: { label: { before: 'a', after: 'b' } }, parent_missing: false },
    { changed_fields: ['label'], changes: { other: { before: 'a', after: 'b' } }, parent_missing: false },
    { changed_fields: ['raw_payload'], changes: { raw_payload: { before: 'a', after: 'b' } }, parent_missing: false },
    { changed_fields: ['system_prompt'], changes: { system_prompt: { before: 'a', after: 'b' } }, parent_missing: false },
  ])('rejects invalid candidate safe diff contracts', (safeDiff) => {
    expect(() => candidateCommandResponseSchema.parse({
      id: 'candidate-1', resource_id: 'skill-1', revision_id: 'revision-2', parent_revision_id: 'revision-1',
      source: 'optimization', status: 'rejected', resource_kind: 'skill', state_version: 2,
      safe_diff: safeDiff, created_at: '2026-01-01T00:00:00Z',
    })).toThrow();
  });

  it.each([
    'api_key=secret', 'API_KEY = secret', 'access_token: secret', 'client_secret = secret',
    'Authorization: Bearer secret', 'authorization = basic abc123',
    'https://example.test?api_key=secret', 'note(api_key=secret)', '{"api_key":"secret"}',
    'prefix?ACCESS_TOKEN=secret', '{"Authorization":"Bearer secret"}',
  ])('rejects sensitive summary value marker %s', (value) => {
    expect(() => safeSummarySchema.parse({ note: value })).toThrow();
  });

  it.each(['token_count=10', 'API key rotation policy', 'authorization guide', 'my_api_key_count=10',
    'my-api_key=metadata', 'api_key_rotation_policy', 'prompt_version=v2'])(
    'allows safe summary wording %s', (value) => {
      expect(safeSummarySchema.parse({ note: value })).toEqual({ note: value });
    },
  );
});

describe('dimensionScoreSchema', () => {
  it('parses a valid dimension', () => {
    const dim = dimensionScoreSchema.parse({ name: 'faithfulness', score: 0.6, passed: true, confidence: 0.9 });
    expect(dim).toEqual({ name: 'faithfulness', score: 0.6, passed: true, confidence: 0.9 });
  });
});

describe('evaluationCaseSchema', () => {
  it('parses tool_spec and step_judge on a case', () => {
    const testCase = evaluationCaseSchema.parse({
      name: '工具链路', input: '查天气', expected_output: '晴天', assertion_mode: 'contains',
      tool_spec: { must_call: ['weather'], must_not_call: ['delete'], order: ['search', 'weather'], max_calls: 5 },
      step_judge: { criteria: '每一步都应给出清晰解释' },
    });
    expect(testCase.tool_spec?.must_call).toEqual(['weather']);
    expect(testCase.tool_spec?.order).toEqual(['search', 'weather']);
    expect(testCase.tool_spec?.max_calls).toBe(5);
    expect(testCase.step_judge?.criteria).toContain('清晰解释');
  });

  it('omits tool_spec and step_judge when the case has none', () => {
    const testCase = evaluationCaseSchema.parse({
      name: '简单', input: 'hi', expected_output: 'hello', assertion_mode: 'exact',
    });
    expect(testCase.tool_spec).toBeUndefined();
    expect(testCase.step_judge).toBeUndefined();
  });
});

describe('evaluationRunSchema', () => {
  it('parses run results with dimensions and failure_reason', () => {
    const run = evaluationRunSchema.parse({
      id: 'r1', resource: { kind: 'skill', resource_id: 's1', revision_id: 'v1' },
      suite_revision_id: 'rev-1', passed: false, total_cases: 1, passed_cases: 0,
      metrics: { version: { suite_revision_id: 'rev-1', platform_seq: 3, resource_version: 'v1' } },
      results: [{ case_id: 'c1', passed: false, process_pass: true, dimensions: [{ name: 'faithfulness', score: 0.3, passed: false }], failure_reason: 'dimension:faithfulness', trace_evidence: { cost_usd: 0.05, latency_ms: 200, success: false, tool_call_count: 3, tool_error_count: 1 } }],
    });
    expect(run.results[0].failure_reason).toBe('dimension:faithfulness');
    expect(run.results[0].dimensions?.[0].score).toBe(0.3);
    expect(run.results[0].trace_evidence?.latency_ms).toBe(200);
  });

  it('parses process_pass, process_failure and the tool sequence on a result', () => {
    const run = evaluationRunSchema.parse({
      id: 'r2', resource: { kind: 'skill', resource_id: 's1', revision_id: 'v1' },
      suite_revision_id: 'rev-1', passed: false, total_cases: 1, passed_cases: 0,
      results: [{
        case_id: 'c1', passed: true, process_pass: false,
        process_failure: 'process:must_not_call:delete',
        tools: [{ tool_name: 'delete', tool_type: 'mcp', step_index: 2, provider_type: 'zhipu',
          capability_id: 'cap-1', arguments: { key: 'value' }, raw_text: '删除一行' }],
      }],
    });
    const result = run.results[0];
    expect(result.process_pass).toBe(false);
    expect(result.process_failure).toBe('process:must_not_call:delete');
    expect(result.tools?.[0].tool_name).toBe('delete');
    expect(result.tools?.[0].arguments).toEqual({ key: 'value' });
    expect(result.tools?.[0].raw_text).toBe('删除一行');
  });
});

describe('observedTraceEvidenceSchema', () => {
  it('parses a valid trace evidence object', () => {
    const ev = observedTraceEvidenceSchema.parse({ cost_usd: 0.05, latency_ms: 200, success: false, tool_call_count: 3, tool_error_count: 1 });
    expect(ev.tool_call_count).toBe(3);
  });
});
