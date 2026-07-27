import { describe, expect, it } from 'vitest';

import { createIdempotencyKey } from './idempotencyKey';

describe('createIdempotencyKey', () => {
  it('returns a key when crypto.randomUUID is unavailable', () => {
    const original = globalThis.crypto;
    Object.defineProperty(globalThis, 'crypto', { value: {}, configurable: true });
    try {
      expect(createIdempotencyKey()).toMatch(/^[0-9a-f-]{36}$/);
    } finally {
      Object.defineProperty(globalThis, 'crypto', { value: original, configurable: true });
    }
  });
});
