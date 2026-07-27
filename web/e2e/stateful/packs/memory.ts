import { expect } from '@playwright/test';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantMutation, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface MemoryPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

export const executeMemoryPack = async ({ actor, pool, evidence, webURL }: MemoryPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const userID = requireUUID(actor.userID ?? '', 'user_id');
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO memory_entries (user_id,session_id,role,content,type,importance)
           VALUES ($1,$2,'user','stateful memory clear evidence','short_term',0.7)`,
    values: [userID, `stateful-${Date.now()}`],
  });
  const page = await actor.context.newPage();
  try {
    await page.goto(`${webURL}/chat`);
    await page.getByRole('button', { name: '打开用户菜单' }).click();
    await page.getByText('清空我的记忆', { exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '清空我的记忆' });
    await expect(dialog.getByText(/不会影响其他团队成员/)).toBeVisible();
    const response = page.waitForResponse((item) => (
      new URL(item.url()).pathname === '/memory/clear' && item.request().method() === 'DELETE'
    ));
    await dialog.getByRole('button', { name: '确认清空' }).click();
    expect((await response).status()).toBe(204);
    const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
      text: 'SELECT count(*)::text AS count FROM memory_entries WHERE user_id=$1', values: [userID],
    });
    expect(result.rows).toEqual([{ count: '0' }]);
    evidence.ui.push('User memory clear completed through Chromium menu and confirmation dialog');
    evidence.http.push('DELETE /memory/clear returned 204');
    evidence.database.push('Generated user memory entries were removed');
  } finally {
    await page.close();
  }
  return ['memory.mutation.delete.memory.clear'];
};
