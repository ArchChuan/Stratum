import { describe, expect, it } from 'vitest';

import { emptyEvidence, summarizeEvidence } from './evidence';

describe('emptyEvidence', () => {
  it('initializes httpRequests as an empty set', () => {
    const evidence = emptyEvidence();
    expect(evidence.httpRequests).toBeInstanceOf(Set);
    expect(evidence.httpRequests.size).toBe(0);
    expect(evidence.ui).toEqual([]);
    expect(evidence.http).toEqual([]);
    expect(evidence.database).toEqual([]);
  });
});

describe('summarizeEvidence', () => {
  it('reports reconciled only when all three layers have evidence', () => {
    expect(summarizeEvidence({
      ui: ['x'], http: ['y'], database: ['z'], httpRequests: new Set(['GET /a']),
    })).toEqual({ ui: 1, http: 1, database: 1, reconciled: true });
    expect(summarizeEvidence({
      ui: ['x'], http: [], database: [], httpRequests: new Set(),
    })).toEqual({ ui: 1, http: 0, database: 0, reconciled: false });
  });
});
