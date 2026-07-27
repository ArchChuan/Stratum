import { describe, expect, it, vi } from 'vitest';

import { createActorContexts } from './actors';
import { assertSafeDatabaseURL, deleteGeneratedActors, requireUUID, withTenantQuery } from './database';
import { redactSensitive } from './redaction';

describe('stateful E2E security boundaries', () => {
  it('redacts credential values recursively without retaining the originals', () => {
    const serialized = JSON.stringify(redactSensitive({
      authorization: 'Bearer fixture-access-token',
      headers: { Cookie: 'refresh_token=fixture-cookie' },
      password: 'fixture-password',
      privateKey: 'fixture-private-key',
      api_key: 'fixture-api-key',
      nested: [{ accessToken: 'fixture-nested-token', safe: 'visible' }],
    }));

    expect(serialized).not.toContain('fixture-');
    expect(serialized).toContain('visible');
  });

  it.each([
    'postgres://user:pass@prod-db.example.com/stratum',
    'postgres://user:pass@db.internal/stratum_production',
    'postgres://user:pass@localhost/postgres',
  ])('rejects a production-like database target before use: %s', (databaseURL) => {
    expect(() => assertSafeDatabaseURL(databaseURL)).toThrow('unsafe E2E database target');
  });

  it('requires UUID parameters and uses a transaction-local tenant search path', async () => {
    expect(() => requireUUID('not-a-uuid', 'tenant_id')).toThrow('tenant_id must be a UUID');
    const query = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce({ rows: [{ count: 1 }] })
      .mockResolvedValueOnce(undefined);
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };

    const result = await withTenantQuery(
      pool,
      '123e4567-e89b-42d3-a456-426614174000',
      { text: 'SELECT count(*) FROM workflow_definitions WHERE id = $1', values: ['id-1'] },
    );

    expect(query.mock.calls).toEqual([
      ['BEGIN'],
      ["SELECT set_config('search_path', $1, true)", ['tenant_123e4567-e89b-42d3-a456-426614174000,public']],
      ['SELECT count(*) FROM workflow_definitions WHERE id = $1', ['id-1']],
      ['ROLLBACK'],
    ]);
    expect(result.rows).toEqual([{ count: 1 }]);
    expect(release).toHaveBeenCalledOnce();
  });

  it('creates a distinct browser context for every system actor', async () => {
    let nextID = 0;
    const browser = { newContext: vi.fn(async () => ({ id: `context-${nextID += 1}` })) };

    const actors = await createActorContexts(browser);

    expect(Object.keys(actors)).toEqual(['systemAdmin', 'tenantAdmin', 'memberA', 'memberB']);
    expect(new Set(Object.values(actors).map((actor) => actor.contextID)).size).toBe(4);
    expect(browser.newContext).toHaveBeenCalledTimes(4);
  });

  it('cleans up only the exact generated user UUIDs', async () => {
    const query = vi.fn().mockResolvedValue({ rowCount: 2 });
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };
    const userIDs = [
      '123e4567-e89b-42d3-a456-426614174000',
      '123e4567-e89b-42d3-a456-426614174001',
    ];

    await deleteGeneratedActors(pool, userIDs);

    expect(query).toHaveBeenCalledWith(
      'DELETE FROM public.users WHERE id = ANY($1::uuid[]) RETURNING id',
      [userIDs],
    );
    expect(release).toHaveBeenCalledOnce();
  });
});
