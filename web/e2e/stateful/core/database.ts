import type { Pool, PoolClient, QueryResult, QueryResultRow } from 'pg';

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const LOCAL_DATABASE_HOSTS = new Set(['127.0.0.1', 'localhost', '::1', 'postgres']);
export const SAFE_POOL_OPTIONS = {
  connectionTimeoutMillis: 5_000,
  query_timeout: 20_000,
  statement_timeout: 20_000,
} as const;

export interface QuerySpec {
  text: string;
  values: unknown[];
}

export interface DatabasePool {
  connect(): Promise<Pick<PoolClient, 'query' | 'release'>>;
}

export const requireUUID = (value: string, field: string): string => {
  if (!UUID_PATTERN.test(value)) throw new Error(`${field} must be a UUID`);
  return value;
};

export const assertSafeDatabaseURL = (raw: string): URL => {
  let databaseURL: URL;
  try {
    databaseURL = new URL(raw);
  } catch {
    throw new Error('unsafe E2E database target');
  }
  const databaseName = databaseURL.pathname.slice(1).toLowerCase();
  if (
    !['postgres:', 'postgresql:'].includes(databaseURL.protocol)
    || !LOCAL_DATABASE_HOSTS.has(databaseURL.hostname.toLowerCase())
    || !/(?:test|e2e)/.test(databaseName)
    || /(?:prod|production)/.test(databaseName)
  ) {
    throw new Error('unsafe E2E database target');
  }
  return databaseURL;
};

export const createSafePool = async (databaseURL: string): Promise<Pool> => {
  assertSafeDatabaseURL(databaseURL);
  const { Pool: PostgreSQLPool } = await import('pg');
  return new PostgreSQLPool({ connectionString: databaseURL, ...SAFE_POOL_OPTIONS });
};

export const withTenantQuery = async <R extends QueryResultRow = QueryResultRow>(
  pool: DatabasePool,
  tenantID: string,
  query: QuerySpec,
): Promise<QueryResult<R>> => {
  requireUUID(tenantID, 'tenant_id');
  if (!query.text.trim() || !Array.isArray(query.values)) throw new Error('parameterized query is required');
  const client = await pool.connect();
  let began = false;
  try {
    await client.query('BEGIN');
    began = true;
    await client.query("SELECT set_config('search_path', $1, true)", [`tenant_${tenantID},public`]);
    return await client.query<R>(query.text, query.values);
  } finally {
    if (began) await client.query('ROLLBACK');
    client.release();
  }
};

export const withTenantMutation = async <R extends QueryResultRow = QueryResultRow>(
  pool: DatabasePool,
  tenantID: string,
  query: QuerySpec,
): Promise<QueryResult<R>> => {
  requireUUID(tenantID, 'tenant_id');
  if (!query.text.trim() || !Array.isArray(query.values)) throw new Error('parameterized query is required');
  const client = await pool.connect();
  let began = false;
  try {
    await client.query('BEGIN');
    began = true;
    await client.query("SELECT set_config('search_path', $1, true)", [`tenant_${tenantID},public`]);
    const result = await client.query<R>(query.text, query.values);
    await client.query('COMMIT');
    began = false;
    return result;
  } catch (error) {
    if (began) await client.query('ROLLBACK');
    throw error;
  } finally {
    client.release();
  }
};

export const elevateGeneratedActor = async (
  pool: DatabasePool,
  tenantID: string,
  userID: string,
  role: 'admin' | 'owner' | 'root',
): Promise<void> => {
  requireUUID(tenantID, 'tenant_id');
  requireUUID(userID, 'user_id');
  const client = await pool.connect();
  try {
    const result = await client.query(
      `WITH membership AS (
         UPDATE public.tenant_members
         SET role = $1
         WHERE tenant_id = $2 AND user_id = $3
         RETURNING user_id
       )
       UPDATE public.users
       SET global_role = CASE WHEN $1 = 'root' THEN 'global_admin' ELSE global_role END
       WHERE id = $3 AND EXISTS (SELECT 1 FROM membership)
       RETURNING id`,
      [role, tenantID, userID],
    );
    if (result.rowCount !== 1) throw new Error('generated actor role elevation did not update exactly one membership');
  } finally {
    client.release();
  }
};

export const setGeneratedActorVerifiedEmail = async (
  pool: DatabasePool,
  userID: string,
  email: string,
): Promise<void> => {
  requireUUID(userID, 'user_id');
  if (!/^[a-z0-9._+-]+@example\.test$/i.test(email)) {
    throw new Error('generated actor email must use example.test');
  }
  const client = await pool.connect();
  try {
    const result = await client.query(
      `UPDATE public.users
       SET email = $1, email_verified_at = now()
       WHERE id = $2 AND is_guest = true
       RETURNING id`,
      [email, userID],
    );
    if (result.rowCount !== 1) {
      throw new Error('generated actor email setup did not update exactly one guest');
    }
  } finally {
    client.release();
  }
};

// addGeneratedActorMembership makes the actor a member of the target tenant
// (#281: every guest owns a private sandbox tenant, so cross-actor fixtures
// must establish membership explicitly). Idempotent for soak re-runs; the
// actor's session is switched by the caller through the real switch-tenant API.
export const addGeneratedActorMembership = async (
  pool: DatabasePool,
  tenantID: string,
  userID: string,
  role: 'member' | 'admin' | 'owner' | 'root',
): Promise<void> => {
  requireUUID(tenantID, 'tenant_id');
  requireUUID(userID, 'user_id');
  const client = await pool.connect();
  try {
    const result = await client.query(
      `INSERT INTO public.tenant_members (tenant_id, user_id, role)
       VALUES ($1, $2, $3)
       ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role
       RETURNING user_id`,
      [tenantID, userID, role],
    );
    if (result.rowCount !== 1) throw new Error('generated actor membership setup did not affect exactly one row');
  } finally {
    client.release();
  }
};

// reEncryptProviderKey saves the synthetic provider's api key through the real
// admin API so the backend performs at-rest encryption itself (#281). Direct
// SQL would leave a legacy plaintext row that the backend refuses to read.
const reEncryptProviderKey = async (adminToken: string, backendURL: string, fixtureURL: string): Promise<void> => {
  const response = await fetch(`${backendURL}/admin/providers/stateful-qwen`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${adminToken}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: 'stateful-qwen',
      kind: 'openai_compat',
      baseUrl: `${fixtureURL}/v1`,
      apiKey: 'stateful-local-provider-key',
      defaultModel: 'qwen-max',
    }),
  });
  if (response.status !== 200) {
    throw new Error(`synthetic LLM provider api key resave failed with status ${response.status}`);
  }
};

export const configureManagedModels = async (
  pool: DatabasePool,
  tenantID: string,
  fixtureURL: string,
  adminToken: string,
  backendURL: string,
): Promise<void> => {
  requireUUID(tenantID, 'tenant_id');
  const client = await pool.connect();
  let began = false;
  try {
    await client.query('BEGIN');
    began = true;
    await client.query("SELECT set_config('search_path', $1, true)", [`tenant_${tenantID},public`]);
    await client.query(
      `DELETE FROM providers
       WHERE tenant_id = $1 AND name LIKE 'E2E-Provider-%'`,
      [tenantID],
    );
    const providerResult = await client.query(
      `INSERT INTO providers (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
       VALUES ($1, $2, 'stateful-qwen', 'openai_compat', $3, $4, 'qwen-max', true)
       ON CONFLICT (tenant_id, name) DO UPDATE SET
         kind = EXCLUDED.kind,
         base_url = EXCLUDED.base_url,
         api_key = EXCLUDED.api_key,
         default_model = EXCLUDED.default_model,
         enabled = true,
         updated_at = now()`,
      ['stateful-qwen', tenantID, `${fixtureURL}/v1`, 'stateful-local-provider-key'],
    );
    if (providerResult.rowCount !== 1) throw new Error('synthetic LLM provider setup did not affect exactly one row');
    const modelResult = await client.query(
      `INSERT INTO models (id, tenant_id, provider_id, name, display_name, capabilities, enabled)
       SELECT 'stateful-' || model.name, $1, $2, model.name, model.name, model.capabilities, true
       FROM (VALUES
         ('qwen-turbo', ARRAY['chat', 'tool_use']::TEXT[]),
         ('qwen-plus', ARRAY['chat', 'tool_use']::TEXT[]),
         ('qwen-max', ARRAY['chat', 'tool_use']::TEXT[]),
         ('text-embedding-v3', ARRAY['embedding']::TEXT[])
       ) AS model(name, capabilities)
       ON CONFLICT (tenant_id, provider_id, name) DO UPDATE SET
         capabilities = EXCLUDED.capabilities,
         enabled = true,
         updated_at = now()`,
      [tenantID, 'stateful-qwen'],
    );
    if (modelResult.rowCount !== 4) throw new Error('synthetic LLM model setup did not affect all expected rows');
    await client.query('COMMIT');
    began = false;
  } catch (error) {
    if (began) await client.query('ROLLBACK');
    throw error;
  } finally {
    client.release();
  }
  // 夹具直写明文 api_key 与 #281 读取端 fail-closed 冲突，须经真实 API 重加密
  //（模拟用户"重新保存 provider api key"流程），否则 agent 执行读到 legacy plaintext 报错。
  await reEncryptProviderKey(adminToken, backendURL, fixtureURL);
};

export const deleteGeneratedActors = async (pool: DatabasePool, userIDs: string[]): Promise<void> => {
  if (userIDs.length === 0) return;
  userIDs.forEach((userID) => requireUUID(userID, 'user_id'));
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query(
      'UPDATE public.tenant_members SET invited_by = NULL WHERE invited_by = ANY($1::uuid[])',
      [userIDs],
    );
    await client.query(
      'DELETE FROM public.tenant_invitations WHERE invited_by = ANY($1::uuid[])',
      [userIDs],
    );
    const result = await client.query(
      'DELETE FROM public.users WHERE id = ANY($1::uuid[]) RETURNING id',
      [userIDs],
    );
    if (result.rowCount !== userIDs.length) {
      throw new Error('generated actor cleanup did not delete every exact user');
    }
    await client.query('COMMIT');
  } catch (error) {
    await client.query('ROLLBACK');
    throw error;
  } finally {
    client.release();
  }
};

export const deleteGeneratedOAuthUser = async (
  pool: DatabasePool,
  githubID: string,
  email: string,
): Promise<void> => {
  validateGeneratedOAuthIdentity(githubID, email);
  const client = await pool.connect();
  try {
    const result = await client.query(
      `DELETE FROM public.users
       WHERE github_id = $1 AND email = $2 AND is_guest = false
       RETURNING id`,
      [githubID, email],
    );
    if (result.rowCount !== 1) throw new Error('generated oauth cleanup did not delete exactly one user');
  } finally {
    client.release();
  }
};

export const deleteGeneratedOAuthUserIfExists = async (
  pool: DatabasePool,
  githubID: string,
  email: string,
): Promise<void> => {
  validateGeneratedOAuthIdentity(githubID, email);
  const client = await pool.connect();
  try {
    await client.query(
      `DELETE FROM public.users
       WHERE github_id = $1 AND email = $2 AND is_guest = false`,
      [githubID, email],
    );
  } finally {
    client.release();
  }
};

const validateGeneratedOAuthIdentity = (githubID: string, email: string): void => {
  if (!/^[1-9][0-9]*$/.test(githubID)) throw new Error('generated oauth github id must be a positive integer');
  if (!/^[a-z0-9._+-]+@example\.test$/i.test(email)) {
    throw new Error('generated oauth email must use example.test');
  }
};

export const suspendDefaultTenant = async (pool: DatabasePool): Promise<string> => {
  const client = await pool.connect();
  try {
    const result = await client.query<{ id: string }>(
      `UPDATE public.tenants
       SET is_default = false
       WHERE is_default = true AND deleted_at IS NULL
       RETURNING id`,
      [],
    );
    if (result.rowCount !== 1) throw new Error('oauth onboarding setup did not suspend exactly one default tenant');
    return requireUUID(result.rows[0].id, 'default_tenant_id');
  } finally {
    client.release();
  }
};

export const restoreDefaultTenant = async (pool: DatabasePool, tenantID: string): Promise<void> => {
  requireUUID(tenantID, 'default_tenant_id');
  const client = await pool.connect();
  try {
    const result = await client.query(
      `UPDATE public.tenants
       SET is_default = true
       WHERE id = $1 AND is_default = false AND deleted_at IS NULL`,
      [tenantID],
    );
    if (result.rowCount !== 1) throw new Error('oauth onboarding setup did not restore the exact default tenant');
  } finally {
    client.release();
  }
};
