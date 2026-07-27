import type { Pool, PoolClient, QueryResult, QueryResultRow } from 'pg';

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const LOCAL_DATABASE_HOSTS = new Set(['127.0.0.1', 'localhost', '::1', 'postgres']);

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
  return new PostgreSQLPool({ connectionString: databaseURL });
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

export const elevateGeneratedActor = async (
  pool: DatabasePool,
  tenantID: string,
  userID: string,
  role: 'admin' | 'root',
): Promise<void> => {
  requireUUID(tenantID, 'tenant_id');
  requireUUID(userID, 'user_id');
  const client = await pool.connect();
  try {
    const result = await client.query(
      'UPDATE public.tenant_members SET role = $1 WHERE tenant_id = $2 AND user_id = $3 RETURNING id',
      [role, tenantID, userID],
    );
    if (result.rowCount !== 1) throw new Error('generated actor role elevation did not update exactly one membership');
  } finally {
    client.release();
  }
};
