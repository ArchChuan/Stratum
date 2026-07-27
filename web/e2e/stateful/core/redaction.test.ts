import { describe, expect, it, vi } from 'vitest';

import { createActorContexts } from './actors';
import {
  assertSafeDatabaseURL,
  deleteGeneratedActors,
  deleteGeneratedOAuthUser,
  requireUUID,
  restoreDefaultTenant,
  SAFE_POOL_OPTIONS,
  setGeneratedActorVerifiedEmail,
  suspendDefaultTenant,
  withTenantQuery,
  withTenantMutation,
} from './database';
import { redactSensitive } from './redaction';

describe('stateful E2E security boundaries', () => {
  it('bounds database connection and query waits', () => {
    expect(SAFE_POOL_OPTIONS.connectionTimeoutMillis).toBeGreaterThan(0);
    expect(SAFE_POOL_OPTIONS.query_timeout).toBeGreaterThan(0);
    expect(SAFE_POOL_OPTIONS.statement_timeout).toBeGreaterThan(0);
  });

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
    expect(requireUUID('0198d0f5-7385-7d9a-8e11-18b0e21fd2b4', 'tenant_id'))
      .toBe('0198d0f5-7385-7d9a-8e11-18b0e21fd2b4');
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

  it('commits parameterized tenant setup mutations and rolls back failures', async () => {
    const committedQuery = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce({ rowCount: 1 })
      .mockResolvedValueOnce(undefined);
    const committedRelease = vi.fn();
    const committedPool = {
      connect: vi.fn().mockResolvedValue({ query: committedQuery, release: committedRelease }),
    };

    await withTenantMutation(committedPool, '123e4567-e89b-42d3-a456-426614174000', {
      text: 'INSERT INTO workflow_runs (id) VALUES ($1)', values: ['123e4567-e89b-42d3-a456-426614174001'],
    });

    expect(committedQuery.mock.calls.map(([text]) => text)).toEqual([
      'BEGIN', "SELECT set_config('search_path', $1, true)",
      'INSERT INTO workflow_runs (id) VALUES ($1)', 'COMMIT',
    ]);
    expect(committedRelease).toHaveBeenCalledOnce();

    const failure = new Error('insert failed');
    const failedQuery = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(undefined);
    const failedRelease = vi.fn();
    const failedPool = { connect: vi.fn().mockResolvedValue({ query: failedQuery, release: failedRelease }) };

    await expect(withTenantMutation(failedPool, '123e4567-e89b-42d3-a456-426614174000', {
      text: 'INSERT INTO workflow_runs (id) VALUES ($1)', values: ['123e4567-e89b-42d3-a456-426614174001'],
    })).rejects.toThrow('insert failed');
    expect(failedQuery).toHaveBeenLastCalledWith('ROLLBACK');
    expect(failedRelease).toHaveBeenCalledOnce();
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
    const query = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce({ rowCount: 1 })
      .mockResolvedValueOnce({ rowCount: 1 })
      .mockResolvedValueOnce({ rowCount: 2 })
      .mockResolvedValueOnce(undefined);
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };
    const userIDs = [
      '123e4567-e89b-42d3-a456-426614174000',
      '123e4567-e89b-42d3-a456-426614174001',
    ];

    await deleteGeneratedActors(pool, userIDs);

    expect(query).toHaveBeenNthCalledWith(
      1,
      'BEGIN',
    );
    expect(query).toHaveBeenNthCalledWith(
      2,
      'UPDATE public.tenant_members SET invited_by = NULL WHERE invited_by = ANY($1::uuid[])',
      [userIDs],
    );
    expect(query).toHaveBeenNthCalledWith(
      3,
      'DELETE FROM public.tenant_invitations WHERE invited_by = ANY($1::uuid[])',
      [userIDs],
    );
    expect(query).toHaveBeenNthCalledWith(
      4,
      'DELETE FROM public.users WHERE id = ANY($1::uuid[]) RETURNING id',
      [userIDs],
    );
    expect(query).toHaveBeenNthCalledWith(5, 'COMMIT');
    expect(release).toHaveBeenCalledOnce();
  });

  it('sets a verified synthetic email for exactly one generated actor', async () => {
    const query = vi.fn().mockResolvedValue({ rowCount: 1 });
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };
    const userID = '123e4567-e89b-42d3-a456-426614174000';

    await setGeneratedActorVerifiedEmail(pool, userID, 'member-a@example.test');

    expect(query).toHaveBeenCalledWith(
      `UPDATE public.users
       SET email = $1, email_verified_at = now()
       WHERE id = $2 AND is_guest = true
       RETURNING id`,
      ['member-a@example.test', userID],
    );
    expect(release).toHaveBeenCalledOnce();
  });

  it('cleans up exactly one generated oauth user by provider identity and email', async () => {
    const query = vi.fn().mockResolvedValue({ rowCount: 1 });
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };

    await deleteGeneratedOAuthUser(pool, '730001', 'stateful-oauth@example.test');

    expect(query).toHaveBeenCalledWith(
      `DELETE FROM public.users
       WHERE github_id = $1 AND email = $2 AND is_guest = false
       RETURNING id`,
      ['730001', 'stateful-oauth@example.test'],
    );
    expect(release).toHaveBeenCalledOnce();
  });

  it('suspends and restores only the exact default tenant around oauth onboarding', async () => {
    const tenantID = '123e4567-e89b-42d3-a456-426614174000';
    const query = vi.fn()
      .mockResolvedValueOnce({ rows: [{ id: tenantID }], rowCount: 1 })
      .mockResolvedValueOnce({ rowCount: 1 });
    const release = vi.fn();
    const pool = { connect: vi.fn().mockResolvedValue({ query, release }) };

    const suspendedID = await suspendDefaultTenant(pool);
    await restoreDefaultTenant(pool, suspendedID);

    expect(query).toHaveBeenNthCalledWith(
      1,
      `UPDATE public.tenants
       SET is_default = false
       WHERE is_default = true AND deleted_at IS NULL
       RETURNING id`,
      [],
    );
    expect(query).toHaveBeenNthCalledWith(
      2,
      `UPDATE public.tenants
       SET is_default = true
       WHERE id = $1 AND is_default = false AND deleted_at IS NULL`,
      [tenantID],
    );
    expect(release).toHaveBeenCalledTimes(2);
  });
});
