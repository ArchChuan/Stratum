import { randomUUID } from 'node:crypto';

import { expect, type BrowserContext } from '@playwright/test';

import {
  elevateGeneratedActor,
  promoteGeneratedActorToGlobalAdmin,
  requireUUID,
  setGeneratedActorVerifiedEmail,
  type DatabasePool,
} from './database';

export type ActorLabel = 'systemAdmin' | 'tenantAdmin' | 'memberA' | 'memberB';

interface BrowserLike<C = BrowserContext> {
  newContext(): Promise<C>;
}

export interface BrowserActor<C = BrowserContext> {
  label: ActorLabel;
  contextID: string;
  context: C;
  tenantID?: string;
  userID?: string;
  email?: string;
  accessToken?: string;
}

interface SessionResponse {
  status(): number;
  json(): Promise<unknown>;
}

interface SessionContext {
  request: {
    post(url: string, options: { data: { tenant_id: string }; headers: { Authorization: string } }):
      Promise<SessionResponse>;
  };
}

const ACTOR_LABELS: ActorLabel[] = ['systemAdmin', 'tenantAdmin', 'memberA', 'memberB'];

export const createActorContexts = async <C>(browser: BrowserLike<C>): Promise<Record<ActorLabel, BrowserActor<C>>> => {
  const entries = await Promise.all(ACTOR_LABELS.map(async (label) => [label, {
    label,
    contextID: randomUUID(),
    context: await browser.newContext(),
  }] as const));
  return Object.fromEntries(entries) as Record<ActorLabel, BrowserActor<C>>;
};

export const restoreActorSession = async <C extends SessionContext>(
  actor: BrowserActor<C>,
  backendURL: string,
): Promise<void> => {
  if (!actor.tenantID || !actor.accessToken) throw new Error(`actor ${actor.label} session cannot be restored`);
  try {
    const response = await actor.context.request.post(`${backendURL}/auth/switch-tenant`, {
      data: { tenant_id: actor.tenantID },
      headers: { Authorization: `Bearer ${actor.accessToken}` },
    });
    if (response.status() !== 200) throw new Error('unexpected response status');
    const body = await response.json() as { access_token?: unknown };
    if (typeof body.access_token !== 'string' || body.access_token.length === 0) {
      throw new Error('missing replacement access token');
    }
    actor.accessToken = body.access_token;
  } catch {
    throw new Error(`actor ${actor.label} session restore failed`);
  }
};

export const withRestoredContextCookies = async <T>(
  context: Pick<BrowserContext, 'cookies' | 'clearCookies' | 'addCookies'>,
  operation: () => Promise<T>,
): Promise<T> => {
  const originalCookies = await context.cookies();
  try {
    return await operation();
  } finally {
    await context.clearCookies();
    if (originalCookies.length > 0) await context.addCookies(originalCookies);
  }
};

interface GuestResponse {
  access_token: string;
  tenant_id: string;
  user: { sub: string };
}

export const createGuestActor = async (
  actor: BrowserActor,
  webURL: string,
  backendURL: string,
  pool: DatabasePool,
): Promise<BrowserActor> => {
  const page = await actor.context.newPage();
  const guestPromise = page.waitForResponse(
    (response) => (
      response.url() === `${backendURL}/auth/guest`
      && response.request().method() === 'POST'
    ),
    { timeout: 15_000 },
  );
  await page.goto(`${webURL}/login`, { waitUntil: 'domcontentloaded', timeout: 15_000 });
  await page.getByRole('tab', { name: '第三方登录' }).click();
  await expect(page.getByRole('button', { name: /快速体验/ })).toBeVisible();
  await page.getByRole('button', { name: /快速体验/ }).click();
  const guest = await guestPromise;
  if (guest.status() !== 201) throw new Error(`guest actor creation failed with status ${guest.status()}`);
  const body = await guest.json() as GuestResponse;
  if (!body.access_token) throw new Error('guest actor access token is missing');
  await expect(page).toHaveURL(`${webURL}/`);
  await page.close();
  const tenantID = requireUUID(body.tenant_id, 'tenant_id');
  const userID = requireUUID(body.user.sub, 'user_id');
  if (actor.label === 'systemAdmin') {
    // 平台管理权限由 users.global_role='global_admin' 表达（前端 /models 路由
    // requiredRole="global_admin"，后端 /admin/* 多走 RequireGlobalAdmin）；own
    // sandbox 租户角色设 owner 而非 root，因为 RequireTenantRole 的 rank 表只认
    // member/admin/owner（root rank=0 直接 403），而后端 /admin/providers 和
    // /admin/models 仍走 RequireTenantRole("member") 供 agent 读取目录。
    await elevateGeneratedActor(pool, tenantID, userID, 'owner');
    await promoteGeneratedActorToGlobalAdmin(pool, userID);
  }
  if (actor.label === 'tenantAdmin') await elevateGeneratedActor(pool, tenantID, userID, 'owner');
  const email = `${actor.label.toLowerCase()}-${userID}@example.test`;
  await setGeneratedActorVerifiedEmail(pool, userID, email);
  let accessToken = body.access_token;
  if (actor.label === 'systemAdmin' || actor.label === 'tenantAdmin') {
    const refreshed = await actor.context.request.post(`${backendURL}/auth/refresh`);
    if (refreshed.status() !== 200) throw new Error(`actor refresh failed with status ${refreshed.status()}`);
    accessToken = (await refreshed.json() as { access_token: string }).access_token;
    if (!accessToken) throw new Error('refreshed actor access token is missing');
  }
  return { ...actor, tenantID, userID, email, accessToken };
};
