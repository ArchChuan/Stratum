import { describe, expect, it } from 'vitest';

import { requireUUID } from './real-platform-assistant';

describe('real platform assistant identifier contract', () => {
  it('accepts UUIDv7 identifiers emitted by the platform', () => {
    expect(() => requireUUID('01981f2e-7b3a-7d32-8a21-123456789abc', 'resource_id')).not.toThrow();
  });

  it('rejects malformed version and variant nibbles', () => {
    expect(() => requireUUID('01981f2e-7b3a-0d32-8a21-123456789abc', 'resource_id')).toThrow();
    expect(() => requireUUID('01981f2e-7b3a-7d32-7a21-123456789abc', 'resource_id')).toThrow();
  });
});
