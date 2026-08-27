import { expect, type Page } from '@playwright/test';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantMutation, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface MemoryPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

const activeUserFacts = `SELECT count(*)::text AS count FROM memory_facts
                         WHERE user_id = $1 AND status = 'active' AND scope = 'user'`;

// memory.route.memory：打开 /memory，断言页面渲染，GET /memory/facts 的 total 与
// memory_facts 中该用户 active 事实数对账（事实 Tab 默认激活并加载该接口）。
const verifyRoute = async (
  page: Page, pool: DatabasePool, tenantID: string, userID: string,
  evidence: EvidenceRecord, webURL: string,
) => {
  const listResponse = page.waitForResponse((item) => {
    const path = new URL(item.url()).pathname;
    const type = item.request().resourceType();
    return path === '/memory/facts' && (type === 'fetch' || type === 'xhr') && item.request().method() === 'GET';
  });
  await page.goto(`${webURL}/memory`);
  await expect(page.locator('.ant-card-head-title').filter({ hasText: '我的记忆' })).toBeVisible();
  // 新版 5-Tab 记忆管理页：统计卡「事实记忆」「话题实体」（spec §1 可视化）。
  await expect(page.getByText('事实记忆', { exact: true })).toBeVisible();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { total: number };
  const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
    text: activeUserFacts, values: [userID],
  });
  expect(body.total).toBe(Number(result.rows[0].count));
  evidence.ui.push('Memory page rendered with stat cards');
  evidence.http.push('GET /memory/facts returned 200 with paginated facts');
  evidence.database.push('Memory facts total reconciled with active user facts');
};

// memory.mutation.delete.memory.fact：插入用户级事实，页面行可见且带删除入口，
// 经 Chromium 确认删除后 DELETE /memory/facts/:id 返回 204，DB 行消失（spec §1 可删除）。
const verifyFactDeletion = async (
  page: Page, pool: DatabasePool, tenantID: string, userID: string,
  evidence: EvidenceRecord, webURL: string,
) => {
  const marker = `stateful-memory-fact-delete-${Date.now()}`;
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO memory_facts (user_id, scope, content, importance, source)
           VALUES ($1, 'user', $2, 0.7, 'manual_api')`,
    values: [userID, marker],
  });
  await page.goto(`${webURL}/memory`);
  const row = page.locator('.ant-table-row').filter({ hasText: marker });
  await expect(row).toBeVisible();
  await expect(row.getByRole('button', { name: '删除' })).toHaveCount(1);
  const deleteResponse = page.waitForResponse((item) => {
    const type = item.request().resourceType();
    return (type === 'fetch' || type === 'xhr') &&
      new URL(item.url()).pathname.startsWith('/memory/facts/') && item.request().method() === 'DELETE';
  });
  await row.getByRole('button', { name: '删除' }).click();
  await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
  expect((await deleteResponse).status()).toBe(204);
  const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
    text: 'SELECT count(*)::text AS count FROM memory_facts WHERE content = $1', values: [marker],
  });
  expect(result.rows).toEqual([{ count: '0' }]);
  evidence.ui.push('Memory fact row rendered with delete affordance and deletion completed through Chromium');
  evidence.http.push('DELETE /memory/facts/:id returned 204');
  evidence.database.push('Inserted memory fact removed after user deletion');
};

export const executeMemoryPack = async ({ actor, pool, evidence, webURL }: MemoryPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const userID = requireUUID(actor.userID ?? '', 'user_id');
  const page = await actor.context.newPage();
  try {
    await verifyRoute(page, pool, tenantID, userID, evidence, webURL);
    await verifyFactDeletion(page, pool, tenantID, userID, evidence, webURL);
    await withTenantMutation(pool, tenantID, {
      text: `INSERT INTO memory_entries (user_id,session_id,role,content,type,importance)
             VALUES ($1,$2,'user','stateful memory clear evidence','short_term',0.7)`,
      values: [userID, `stateful-${Date.now()}`],
    });
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
    return ['memory.route.memory', 'memory.mutation.delete.memory.fact', 'memory.mutation.delete.memory.clear'];
  } finally {
    await page.close();
  }
};
