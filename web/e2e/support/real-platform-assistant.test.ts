import { describe, expect, it } from 'vitest';

import { requireCleanupTenantKind, requireUUID } from './real-platform-assistant';

describe('real platform assistant identifier contract', () => {
  it('accepts UUIDv7 identifiers emitted by the platform', () => {
    expect(() => requireUUID('01981f2e-7b3a-7d32-8a21-123456789abc', 'resource_id')).not.toThrow();
  });

  it('rejects malformed version and variant nibbles', () => {
    expect(() => requireUUID('01981f2e-7b3a-0d32-8a21-123456789abc', 'resource_id')).toThrow();
    expect(() => requireUUID('01981f2e-7b3a-7d32-7a21-123456789abc', 'resource_id')).toThrow();
  });
});

describe('real platform assistant cleanup tenant contract', () => {
  it('allows user-only cleanup only for exactly one default tenant row', () => {
    expect(() => requireCleanupTenantKind('true', true)).not.toThrow();
    expect(() => requireCleanupTenantKind('', true)).toThrow(/missing/);
    expect(() => requireCleanupTenantKind('false', true)).toThrow(/default/);
    expect(() => requireCleanupTenantKind('true\ntrue', true)).toThrow(/ambiguous/);
  });

  it('allows tenant-level cleanup only for exactly one non-default tenant row', () => {
    expect(() => requireCleanupTenantKind('false', false)).not.toThrow();
    expect(() => requireCleanupTenantKind('', false)).toThrow(/missing/);
    expect(() => requireCleanupTenantKind('true', false)).toThrow(/default/);
    expect(() => requireCleanupTenantKind('false\nfalse', false)).toThrow(/ambiguous/);
  });
});
