import { expect, type Page } from '@playwright/test';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantMutation, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface MemoryPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

const activeUserFacts = `SELECT count(*)::text AS count FROM memory_facts
                         WHERE user_id = $1 AND status = 'active' AND scope = 'user'`;

// memory.route.memory：打开 /memory，断言页面渲染，GET /memory 的 total 与
// memory_facts 中该用户 active 事实数对账（列表端点同口径分页）。
const verifyRoute = async (
  page: Page, pool: DatabasePool, tenantID: string, userID: string,
  evidence: EvidenceRecord, webURL: string,
) => {
  // /memory 同时是 SPA 路由（导航返回 index.html），API 请求是 fetch/xhr，
  // 必须按 resourceType 区分，否则会匹配到导航的 HTML 响应。
  const listResponse = page.waitForResponse((item) => {
    const path = new URL(item.url()).pathname;
    const type = item.request().resourceType();
    return path === '/memory' && (type === 'fetch' || type === 'xhr') && item.request().method() === 'GET';
  });
  await page.goto(`${webURL}/memory`);
  await expect(page.locator('.ant-card-head-title').filter({ hasText: '我的记忆' })).toBeVisible();
  await expect(page.getByText('记忆条目', { exact: true })).toBeVisible();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { total: number };
  const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
    text: activeUserFacts, values: [userID],
  });
  expect(body.total).toBe(Number(result.rows[0].count));
  evidence.ui.push('Memory page rendered with stats cards');
  evidence.http.push('GET /memory returned 200 with paginated facts');
  evidence.database.push('Memory list total reconciled with active user facts');
};

// memory.delete.memory：插入用户级事实，页面行内 DangerPopconfirm 删除，
// DELETE /memory/:id 返回 204 后对账 memory_facts 该行已移除。
const verifyDelete = async (
  page: Page, pool: DatabasePool, tenantID: string, userID: string,
  evidence: EvidenceRecord, webURL: string,
) => {
  const marker = `stateful-memory-delete-evidence-${Date.now()}`;
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const observedDeletes: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  page.on('request', (req) => {
    if (req.method() === 'DELETE' && req.url().includes('/memory')) {
      observedDeletes.push(`${req.method()} ${req.url()}`);
    }
  });
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO memory_facts (user_id, scope, content, importance, source)
           VALUES ($1, 'user', $2, 0.7, 'manual_api')`,
    values: [userID, marker],
  });
  const deleteResponse = page.waitForResponse((item) => {
    const path = new URL(item.url()).pathname;
    return path.match(/^\/memory\/[^/]+$/) !== null && !path.endsWith('/clear') && item.request().method() === 'DELETE';
  });
  // 立即挂一个空 catch 消费 rejection：若超时发生在 await 之前，
  // 未处理的 rejection 会让 Playwright 中断测试并取消后续 locator 操作。
  deleteResponse.catch(() => undefined);
  await page.reload();
  const row = page.locator('.ant-table-row').filter({ hasText: marker });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: '删除' }).click();
  const confirm = page.locator('.ant-popover:visible').filter({ hasText: '删除这条记忆？' });
  await expect(confirm).toBeVisible();
  // antd 按钮的 accessible name 是「删 除」（文本节点间有空格），exact/子串匹配均失败，
  // 用 \s* 正则归一空白后匹配确认按钮。
  const okButton = confirm.getByRole('button', { name: /删\s*除/ });
  await okButton.click();
  const deletedResponse = await deleteResponse.catch(() => null);
  if (deletedResponse === null) {
    // 请求未发出的可诊断信息：页面 JS 错误 + 观测到的 DELETE 请求 + popover DOM 状态。
    const popoverDump = await page.evaluate(() => {
      const pops = [...document.querySelectorAll('.ant-popover')];
      return pops.map((p) => {
        const rect = p.getBoundingClientRect();
        const buttons = [...p.querySelectorAll('button')].map((b) => {
          const r = b.getBoundingClientRect();
          return `${b.textContent?.trim()}[x=${Math.round(r.x)},y=${Math.round(r.y)},w=${Math.round(r.width)},h=${Math.round(r.height)},disabled=${b.disabled}]`;
        });
        return `{rect=${Math.round(rect.width)}x${Math.round(rect.height)},buttons=[${buttons.join(';')}]}`;
      });
    });
    await page.screenshot({ path: '/tmp/mem-popover.png' });
    throw new Error(
      'DELETE /memory/:id never observed. '
      + `popoverDump=${JSON.stringify(popoverDump)} `
      + `pageErrors=${JSON.stringify(pageErrors)} consoleErrors=${JSON.stringify(consoleErrors)} `
      + `observedDeletes=${JSON.stringify(observedDeletes)}`,
    );
  }
  expect(deletedResponse.status()).toBe(204);
  const deleted = await withTenantQuery<{ count: string }>(pool, tenantID, {
    text: 'SELECT count(*)::text AS count FROM memory_facts WHERE content = $1', values: [marker],
  });
  expect(deleted.rows).toEqual([{ count: '0' }]);
  evidence.ui.push('Memory fact deleted through row confirmation');
  evidence.http.push('DELETE /memory/:id returned 204');
  evidence.database.push('Deleted memory fact removed from memory_facts');
};

export const executeMemoryPack = async ({ actor, pool, evidence, webURL }: MemoryPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const userID = requireUUID(actor.userID ?? '', 'user_id');
  const page = await actor.context.newPage();
  try {
    await verifyRoute(page, pool, tenantID, userID, evidence, webURL);
    await verifyDelete(page, pool, tenantID, userID, evidence, webURL);
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
    return ['memory.route.memory', 'memory.delete.memory', 'memory.mutation.delete.memory.clear'];
  } finally {
    await page.close();
  }
};
