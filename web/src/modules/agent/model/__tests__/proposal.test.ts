import { describe, expect, it } from 'vitest';

import { resourceChangeProposalSchema } from '../proposal';

const base = {
  id: 'proposal-1', proposerId: 'admin-1', operation: 'update', resourceId: 'skill-1',
  summary: 'update skill_draft', status: 'ready_for_review', events: [], editCount: 2,
  expiresAt: '2026-07-28T00:00:00Z', createdAt: '2026-07-27T00:00:00Z', updatedAt: '2026-07-27T00:00:00Z',
};

describe('resourceChangeProposalSchema', () => {
  it('parses a supported skill draft payload without temperature', () => {
    const parsed = resourceChangeProposalSchema.parse({
      ...base,
      resourceKind: 'skill_draft',
      baselineProjection: { name: '旧名称', description: '旧说明', instructions: '旧指令' },
      payload: { name: '检索助手', description: '查找资料', instructions: '只引用已核验来源' },
    });
    expect(parsed.resourceKind).toBe('skill_draft');
    if (parsed.resourceKind !== 'skill_draft') throw new Error('unexpected proposal kind');
    expect(parsed.payload.instructions).toBe('只引用已核验来源');
    expect(parsed.baselineProjection?.instructions).toBe('旧指令');
    expect(parsed.editCount).toBe(2);
  });

  it.each(['temperature', 'token', 'apiKey', 'headers', 'env'])('rejects unsupported or secret field %s', (field) => {
    expect(() => resourceChangeProposalSchema.parse({
      ...base,
      resourceKind: 'skill_draft',
      payload: { name: '检索助手', description: '查找资料', instructions: '执行', [field]: 'marker' },
    })).toThrow();
  });
});
