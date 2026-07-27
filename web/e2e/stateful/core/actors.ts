import { randomUUID } from 'node:crypto';

import type { BrowserContext } from '@playwright/test';

import { elevateGeneratedActor, requireUUID, type DatabasePool } from './database';

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

interface GuestResponse {
  tenant_id: string;
  user: { sub: string };
}

export const createGuestActor = async (
  actor: BrowserActor,
  backendURL: string,
  pool: DatabasePool,
): Promise<BrowserActor> => {
  const guest = await actor.context.request.post(`${backendURL}/auth/guest`);
  if (guest.status() !== 201) throw new Error(`guest actor creation failed with status ${guest.status()}`);
  const body = await guest.json() as GuestResponse;
  const tenantID = requireUUID(body.tenant_id, 'tenant_id');
  const userID = requireUUID(body.user.sub, 'user_id');
  if (actor.label === 'systemAdmin') await elevateGeneratedActor(pool, tenantID, userID, 'root');
  if (actor.label === 'tenantAdmin') await elevateGeneratedActor(pool, tenantID, userID, 'owner');
  if (actor.label === 'systemAdmin' || actor.label === 'tenantAdmin') {
    const refreshed = await actor.context.request.post(`${backendURL}/auth/refresh`);
    if (refreshed.status() !== 200) throw new Error(`actor refresh failed with status ${refreshed.status()}`);
  }
  return { ...actor, tenantID, userID };
};
