import { describe, expect, it } from 'vitest';

import { parseRuntimeOptions, SYSTEM_PACKS } from './runtime';

describe('stateful runtime options', () => {
  it('defaults to the full short headless acceptance scope', () => {
    const options = parseRuntimeOptions({});
    expect(options).toMatchObject({ mode: 'short', durationSeconds: 600, seed: 1 });
    expect(options.packs).toEqual(SYSTEM_PACKS);
  });

  it('defaults soak acceptance to the 600-second test profile', () => {
    expect(parseRuntimeOptions({ STATEFUL_E2E_MODE: 'soak' })).toMatchObject({
      mode: 'soak', acceptanceProfile: 'test', durationSeconds: 600,
    });
  });

  it('defaults release acceptance to 3600 seconds', () => {
    expect(parseRuntimeOptions({ STATEFUL_E2E_MODE: 'soak', STATEFUL_E2E_PROFILE: 'release' })).toMatchObject({
      acceptanceProfile: 'release', durationSeconds: 3600,
    });
  });

  it.each([
    [{ STATEFUL_E2E_MODE: 'unknown' }, 'unsupported'],
    [{ STATEFUL_E2E_DURATION_SEC: '599' }, 'between 600 and 14400'],
    [{ STATEFUL_E2E_DURATION_SEC: '14401' }, 'between 600 and 14400'],
    [{ STATEFUL_E2E_MODE: 'short', STATEFUL_E2E_PROFILE: 'test' }, 'short mode'],
    [{ STATEFUL_E2E_MODE: 'soak', STATEFUL_E2E_PROFILE: 'unknown' }, 'acceptance profile'],
    [{ STATEFUL_E2E_MODE: 'soak', STATEFUL_E2E_PROFILE: 'release', STATEFUL_E2E_DURATION_SEC: '3599' }, 'release minimum'],
    [{ STATEFUL_E2E_PACKS: 'iam,unknown' }, 'unknown stateful E2E pack'],
    [{ STATEFUL_E2E_SEED: '-1' }, 'unsigned 32-bit'],
  ])('rejects invalid runtime input %#', (environment, message) => {
    expect(() => parseRuntimeOptions(environment)).toThrow(message);
  });
});
