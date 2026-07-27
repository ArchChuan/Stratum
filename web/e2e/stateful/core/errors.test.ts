import { describe, expect, it } from 'vitest';

import { acceptanceError, acceptanceErrors } from './errors';

describe('stateful acceptance error reporting', () => {
  it('preserves execution and cleanup failures together', () => {
    const execution = new Error('execution failed');
    const cleanup = new Error('cleanup failed');

    const result = acceptanceError(execution, cleanup);

    expect(result).toBeInstanceOf(AggregateError);
    expect((result as AggregateError).errors).toEqual([execution, cleanup]);
  });

  it('returns the single failure without wrapping it', () => {
    const execution = new Error('execution failed');
    const cleanup = new Error('cleanup failed');

    expect(acceptanceError(execution, undefined)).toBe(execution);
    expect(acceptanceError(undefined, cleanup)).toBe(cleanup);
    expect(acceptanceError(undefined, undefined)).toBeUndefined();
  });

  it('aggregates every defined failure while preserving a single error', () => {
    const first = new Error('first');
    const second = new Error('second');
    const third = new Error('third');

    expect(acceptanceErrors([undefined, first])).toBe(first);
    const combined = acceptanceErrors([first, undefined, second, third]);
    expect(combined).toBeInstanceOf(AggregateError);
    expect((combined as AggregateError).errors).toEqual([first, second, third]);
  });
});
