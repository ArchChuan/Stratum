import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { parseRuntimeOptions, SYSTEM_PACKS } from './runtime';

const runtimeURLs = {
  E2E_API_URL: 'http://127.0.0.1:38080',
  E2E_WEB_URL: 'http://127.0.0.1:35173',
  E2E_FIXTURE_URL: 'http://127.0.0.1:39091',
};

describe('stateful runtime options', () => {
  it('defaults to the full short headless acceptance scope', () => {
    const options = parseRuntimeOptions(runtimeURLs);
    expect(options).toMatchObject({ mode: 'short', durationSeconds: 600, seed: 1 });
    expect(options.packs).toEqual(SYSTEM_PACKS);
  });

  it('defaults soak acceptance to the 600-second test profile', () => {
    expect(parseRuntimeOptions({ ...runtimeURLs, STATEFUL_E2E_MODE: 'soak' })).toMatchObject({
      mode: 'soak', acceptanceProfile: 'test', durationSeconds: 600,
    });
  });

  it('defaults release acceptance to 3600 seconds', () => {
    expect(parseRuntimeOptions({ ...runtimeURLs, STATEFUL_E2E_MODE: 'soak', STATEFUL_E2E_PROFILE: 'release' })).toMatchObject({
      acceptanceProfile: 'release', durationSeconds: 3600,
    });
  });

  it.each([
    [{ ...runtimeURLs, E2E_API_URL: undefined }, 'E2E_API_URL is required'],
    [{ ...runtimeURLs, E2E_API_URL: 'https://127.0.0.1:38080' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://localhost:38080' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://127.0.0.1:0' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://user:secret@127.0.0.1:38080' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://127.0.0.1:38080/api' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://127.0.0.1:38080?debug=true' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_API_URL: 'http://127.0.0.1:38080#fragment' }, 'explicit 127.0.0.1 HTTP'],
    [{ ...runtimeURLs, E2E_FIXTURE_URL: runtimeURLs.E2E_API_URL }, 'role ports must be distinct'],
  ])('rejects unsafe or ambiguous runtime URLs %#', (environment, message) => {
    expect(() => parseRuntimeOptions(environment)).toThrow(message);
  });

  it('keeps fixed stateful ports out of the runner and production packs', () => {
    const packs = resolve(process.cwd(), 'e2e/stateful/packs');
    const files = [
      resolve(process.cwd(), '../scripts/e2e/system-stateful.sh'),
      ...readdirSync(packs).filter((name) => name.endsWith('.ts')).map((name) => resolve(packs, name)),
    ];
    for (const file of files) expect(readFileSync(file, 'utf8')).not.toMatch(/15173|18080|19090|19091/);
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
