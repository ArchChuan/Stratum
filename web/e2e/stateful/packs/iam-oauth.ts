import { expect, type BrowserContext, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import {
  deleteGeneratedOAuthUser,
  requireUUID,
  restoreDefaultTenant,
  suspendDefaultTenant,
  type DatabasePool,
} from '../core/database';
import { acceptanceError } from '../core/errors';
import type { EvidenceRecord } from '../core/evidence';

interface OAuthJourneyContext {
  context: BrowserContext;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  backendURL: string;
}

interface OAuthIdentity {
  githubID: string;
  login: string;
  email: string;
}

const waitForRequest = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));

const publicQuery = async <R extends QueryResultRow>(
  pool: DatabasePool,
  text: string,
  values: unknown[],
): Promise<R[]> => {
  const client = await pool.connect();
  try {
    return (await client.query(text, values)).rows as R[];
  } finally {
    client.release();
  }
};

const oauthIdentity = (): OAuthIdentity => {
  const githubID = process.env.E2E_GITHUB_ID ?? '';
  const login = process.env.E2E_GITHUB_LOGIN ?? '';
  const email = process.env.E2E_GITHUB_EMAIL ?? '';
  if (!/^[1-9][0-9]*$/.test(githubID)) throw new Error('E2E_GITHUB_ID must be a positive integer');
  if (!/^stateful-oauth-[a-z0-9-]+$/i.test(login)) throw new Error('unsafe generated oauth login');
  if (!/^stateful-oauth-[a-z0-9-]+@example\.test$/i.test(email)) throw new Error('unsafe generated oauth email');
  return { githubID, login, email };
};

export const executeIAMOAuthJourney = async ({
  context, pool, evidence, webURL, backendURL,
}: OAuthJourneyContext): Promise<string[]> => {
  const identity = oauthIdentity();
  const page = await context.newPage();
  const completed: string[] = [];
  const tenantName = `E2E-OAuth-${Date.now()}`;
  let registered = false;
  let executionError: unknown;
  try {
    const defaultTenantID = await suspendDefaultTenant(pool);
    const firstExchange = waitForRequest(page, '/auth/oauth/exchange', 'POST');
    try {
      await page.goto(`${backendURL}/auth/github`);
      expect((await firstExchange).status()).toBe(200);
      await expect(page).toHaveURL(`${webURL}/onboarding`);
    } finally {
      await restoreDefaultTenant(pool, defaultTenantID);
    }
    await expect(page.getByRole('heading', { name: '欢迎使用 Stratum' })).toBeVisible();
    completed.push('iam.route.auth.callback', 'iam.mutation.post.auth.oauth.exchange');

    await page.getByLabel('租户名称').fill(tenantName);
    const registerResponse = waitForRequest(page, '/auth/register', 'POST');
    await page.getByRole('button', { name: '创建租户' }).click();
    const registeredResponse = await registerResponse;
    expect(registeredResponse.status()).toBe(201);
    const tenantID = requireUUID(
      (await registeredResponse.json() as { tenant_id: string }).tenant_id,
      'oauth_tenant_id',
    );
    registered = true;
    await expect(page).toHaveURL(`${webURL}/`);
    await expect(page.getByRole('button', { name: '打开用户菜单' })).toBeVisible();
    const registrationRows = await publicQuery<{
      github_id: string;
      email: string;
      verified: boolean;
      tenant_name: string;
      role: string;
    }>(pool,
      `SELECT u.github_id, u.email, u.email_verified_at IS NOT NULL AS verified,
              t.name AS tenant_name, tm.role
       FROM public.users u
       JOIN public.tenant_members tm ON tm.user_id = u.id
       JOIN public.tenants t ON t.id = tm.tenant_id
       WHERE u.github_id = $1 AND u.email = $2 AND t.id = $3`,
      [identity.githubID, identity.email, tenantID]);
    expect(registrationRows).toEqual([{
      github_id: identity.githubID,
      email: identity.email,
      verified: true,
      tenant_name: tenantName,
      role: 'owner',
    }]);
    evidence.ui.push('new oauth identity completed onboarding in Chromium');
    evidence.http.push('oauth exchange and registration returned success');
    evidence.database.push('verified oauth user, tenant, and owner membership reconciled');
    completed.push('iam.mutation.post.auth.register');

    await page.goto(`${webURL}/tenant/settings`);
    await expect(page.getByRole('heading', { name: '租户设置' })).toBeVisible();
    await page.locator('.tenant-embedding-controls .ant-select').click();
    await page.getByText(/text-embedding-v3（推荐/).click();
    const embedResponse = waitForRequest(page, '/tenant/embed-model', 'PATCH');
    await page.getByRole('button', { name: '确认设置' }).click();
    expect((await embedResponse).status()).toBe(200);
    await expect(page.getByText('当前嵌入模型：')).toBeVisible();
    await page.reload();
    await expect(page.getByText('text-embedding-v3', { exact: true })).toBeVisible();
    const embedRows = await publicQuery<{ embed_model: string }>(pool,
      `SELECT settings->>'embed_model' AS embed_model FROM public.tenants WHERE id = $1`,
      [tenantID]);
    expect(embedRows).toEqual([{ embed_model: 'text-embedding-v3' }]);
    evidence.ui.push('owner configured a set-once embedding model and refreshed');
    evidence.http.push('embed-model mutation returned 200');
    evidence.database.push('tenant embedding model reconciled');
    completed.push('iam.mutation.patch.tenant.embed.model');

    await page.getByRole('button', { name: '打开用户菜单' }).click();
    const logoutResponse = waitForRequest(page, '/auth/logout', 'POST');
    await page.getByRole('menuitem', { name: '退出登录' }).click();
    expect((await logoutResponse).status()).toBe(200);
    await expect(page).toHaveURL(`${webURL}/login`);
    const rejectedRefresh = await context.request.post(`${backendURL}/auth/refresh`);
    expect(rejectedRefresh.status()).toBe(401);
    completed.push('iam.mutation.post.auth.logout');

    const returningExchange = waitForRequest(page, '/auth/oauth/exchange', 'POST');
    await page.goto(`${backendURL}/auth/github`);
    expect((await returningExchange).status()).toBe(200);
    await expect(page).toHaveURL(`${webURL}/`);
    await expect(page.getByRole('button', { name: '打开用户菜单' })).toBeVisible();
    const userCountRows = await publicQuery<{ count: string }>(pool,
      'SELECT count(*)::text AS count FROM public.users WHERE github_id = $1', [identity.githubID]);
    expect(userCountRows).toEqual([{ count: '1' }]);
    evidence.ui.push('returning oauth identity restored its authenticated session');
    evidence.http.push('returning oauth exchange returned login kind');
    evidence.database.push('returning oauth login reused exactly one user');

    await page.goto(`${webURL}/tenant/settings`);
    await page.getByRole('button', { name: '删除租户' }).click();
    const confirmation = page.getByRole('dialog', { name: '删除租户' });
    await expect(confirmation).toBeVisible();
    const deleteResponse = waitForRequest(page, '/tenant', 'DELETE');
    await confirmation.getByRole('button', { name: '确认删除' }).click();
    expect((await deleteResponse).status()).toBe(200);
    const tenantRows = await publicQuery<{ count: string }>(pool,
      'SELECT count(*)::text AS count FROM public.tenants WHERE id = $1', [tenantID]);
    expect(tenantRows).toEqual([{ count: '0' }]);
    evidence.ui.push('owner confirmed deletion of the disposable oauth tenant');
    evidence.http.push('tenant self-delete returned 200');
    evidence.database.push('oauth tenant deletion reconciled');
    completed.push('iam.mutation.delete.tenant');
  } catch (error) {
    executionError = error;
  }

  let cleanupError: unknown;
  try {
    await page.close();
  } catch (error) {
    cleanupError = error;
  }
  if (registered) {
    try {
      await deleteGeneratedOAuthUser(pool, identity.githubID, identity.email);
    } catch (error) {
      cleanupError = acceptanceError(cleanupError, error);
    }
  }
  const failure = acceptanceError(executionError, cleanupError);
  if (failure) throw failure;
  return completed;
};
