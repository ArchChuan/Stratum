import { describe, expect, it } from 'vitest';

import { resolveE2EMCPPort } from './endpoints';

describe('stateful E2E endpoints', () => {
  it.each([
    { name: 'uses the default when unset', raw: undefined, expected: 19091 },
    { name: 'accepts a non-privileged loopback port', raw: '19092', expected: 19092 },
  ])('$name', ({ raw, expected }) => {
    expect(resolveE2EMCPPort(raw)).toBe(expected);
  });

  it.each(['', 'abc', '1023', '65536'])('rejects an unsafe MCP port: %s', (raw) => {
    expect(() => resolveE2EMCPPort(raw)).toThrow('STATEFUL_E2E_MCP_PORT must be between 1024 and 65535');
  });
});
