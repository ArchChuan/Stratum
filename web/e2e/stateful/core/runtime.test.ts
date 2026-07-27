import { describe, expect, it } from 'vitest';

import { parseRuntimeOptions, SYSTEM_PACKS } from './runtime';

describe('stateful runtime options', () => {
  it('defaults to the full short headless acceptance scope', () => {
    const options = parseRuntimeOptions({});
    expect(options).toMatchObject({ mode: 'short', durationSeconds: 600, seed: 1 });
    expect(options.packs).toEqual(SYSTEM_PACKS);
  });

  it.each([
    [{ STATEFUL_E2E_MODE: 'unknown' }, 'unsupported'],
    [{ STATEFUL_E2E_DURATION_SEC: '599' }, 'between 600 and 14400'],
    [{ STATEFUL_E2E_DURATION_SEC: '14401' }, 'between 600 and 14400'],
    [{ STATEFUL_E2E_PACKS: 'iam,unknown' }, 'unknown stateful E2E pack'],
    [{ STATEFUL_E2E_SEED: '-1' }, 'unsigned 32-bit'],
  ])('rejects invalid runtime input %#', (environment, message) => {
    expect(() => parseRuntimeOptions(environment)).toThrow(message);
  });
});
