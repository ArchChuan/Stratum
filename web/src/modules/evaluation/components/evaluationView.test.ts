import { describe, expect, it } from 'vitest';

import { runDisplayStatus } from './evaluationView';

describe('runDisplayStatus', () => {
  it('distinguishes worker completion from business pass', () => {
    expect(runDisplayStatus('succeeded', false)).toBe('failed');
    expect(runDisplayStatus('succeeded', true)).toBe('passed');
    expect(runDisplayStatus('running', false)).toBe('running');
  });
});
