import { expect, type Page } from '@playwright/test';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface PromptPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

// prompt_templates / prompt_bindings 位于 public schema（非 tenant schema），
// 对账 SQL 使用 schema-qualified 名称，口径与后端 ListByKey 一致
// （tenant_id IS NOT DISTINCT FROM，COUNT(DISTINCT key)）。
const distinctPromptKeys = `SELECT count(*)::text AS total FROM (
  SELECT DISTINCT ON (key) key FROM public.prompt_templates WHERE tenant_id = $1
) t`;

// prompt.route.prompts：打开 /prompts，断言页面渲染，GET /prompts 的 total 与
// public.prompt_templates 该租户 DISTINCT key 数对账。
const verifyRoute = async (
  page: Page, pool: DatabasePool, tenantID: string, evidence: EvidenceRecord, webURL: string,
) => {
  // /prompts 同时是 SPA 路由（导航返回 index.html），API 请求是 fetch/xhr，
  // 必须按 resourceType 区分，否则会匹配到导航的 HTML 响应。
  const listResponse = page.waitForResponse((item) => {
    const path = new URL(item.url()).pathname;
    const type = item.request().resourceType();
    return path === '/prompts' && (type === 'fetch' || type === 'xhr') && item.request().method() === 'GET';
  });
  await page.goto(`${webURL}/prompts`);
  // 侧边栏菜单链接与页面标题同名，用 heading role 锚定页面标题。
  await expect(page.getByRole('heading', { name: '提示词管理' })).toBeVisible();
  await expect(page.getByRole('button', { name: '新建模板' })).toBeVisible();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { total: number };
  const result = await withTenantQuery<{ total: string }>(pool, tenantID, {
    text: distinctPromptKeys, values: [tenantID],
  });
  expect(body.total).toBe(Number(result.rows[0].total));
  evidence.ui.push('Prompt list page rendered with template cards');
  evidence.http.push('GET /prompts returned 200 with template summaries');
  evidence.database.push('Prompt template summaries reconciled with public.prompt_templates');
};

// prompt.create.template：弹窗创建模板，POST /prompts 返回 201 后对账
// prompt_templates 该 key 首版本（v1 draft）。
const verifyCreate = async (
  page: Page, pool: DatabasePool, tenantID: string,
  evidence: EvidenceRecord,
): Promise<string> => {
  const key = `stateful_prompt_ev_${Date.now()}`;
  const content = 'Stateful E2E prompt evidence template';
  const createdResponse = page.waitForResponse((item) => (
    new URL(item.url()).pathname === '/prompts' && item.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: '新建模板' }).click();
  const modal = page.getByRole('dialog');
  await expect(modal).toBeVisible();
  await modal.getByLabel('模板 Key').fill(key);
  await modal.getByLabel('模板内容').fill(content);
  // antd Button insertSpace：两字按钮「创建」的 accessible name 是「创 建」，
  // 用 \s* 正则归一空白后匹配确认按钮。
  await modal.getByRole('button', { name: /创\s*建/ }).click();
  const res = await createdResponse;
  expect(res.status()).toBe(201);
  const rows = await withTenantQuery<{ version: string; status: string }>(pool, tenantID, {
    text: 'SELECT version::text AS version, status FROM public.prompt_templates WHERE key = $1 AND tenant_id = $2',
    values: [key, tenantID],
  });
  expect(rows.rows).toEqual([{ version: '1', status: 'draft' }]);
  await expect(page.locator('.ant-card').filter({ hasText: key })).toBeVisible();
  evidence.ui.push('Prompt template created through modal form');
  evidence.http.push('POST /prompts returned 201');
  evidence.database.push('prompt_templates row exists with new key version 1 draft');
  return key;
};

// prompt.publish.version：打开模板抽屉，发布 v1，POST publish 返回 200 后
// 对账该版本 status 变为 published。
const verifyPublish = async (
  page: Page, pool: DatabasePool, tenantID: string, key: string,
  evidence: EvidenceRecord,
) => {
  await page.locator('.ant-card').filter({ hasText: key }).click();
  await expect(page.getByText(`提示词模板 · ${key}`)).toBeVisible();
  const versionRow = page.locator('.ant-table-row').filter({ hasText: 'v1' });
  await expect(versionRow).toBeVisible();
  const publishResponse = page.waitForResponse((item) => (
    item.request().method() === 'POST'
    && new URL(item.url()).pathname.match(/^\/prompts\/[^/]+\/versions\/1\/publish$/) !== null
  ));
  // 「发布」同样是两字按钮（insertSpace → 「发 布」），正则匹配。
  await versionRow.getByRole('button', { name: /发\s*布/ }).click();
  expect((await publishResponse).status()).toBe(200);
  const rows = await withTenantQuery<{ status: string }>(pool, tenantID, {
    text: 'SELECT status FROM public.prompt_templates WHERE key = $1 AND version = 1 AND tenant_id = $2',
    values: [key, tenantID],
  });
  expect(rows.rows).toEqual([{ status: 'published' }]);
  evidence.ui.push('Prompt version published from drawer');
  evidence.http.push('POST /prompts/:key/versions/:version/publish returned 200');
  evidence.database.push('Prompt template version status becomes published');
};

// prompt.upsert.binding：抽屉 A/B 表单选稳定版本并保存，PUT /prompts/bindings
// 返回 200 后对账 prompt_bindings 该 key+scope 行。
const verifyUpsertBinding = async (
  page: Page, pool: DatabasePool, tenantID: string, key: string,
  evidence: EvidenceRecord,
) => {
  const hashRow = await withTenantQuery<{ content_hash: string }>(pool, tenantID, {
    text: 'SELECT content_hash FROM public.prompt_templates WHERE key = $1 AND version = 1 AND tenant_id = $2',
    values: [key, tenantID],
  });
  const hash = hashRow.rows[0].content_hash;
  const stableSelect = page.locator('.ant-form-item').filter({ hasText: '稳定版本' }).locator('.ant-select');
  await stableSelect.click();
  await page
    .locator('.ant-select-dropdown:visible .ant-select-item-option')
    .filter({ hasText: 'v1' })
    .first()
    .click();
  const savedResponse = page.waitForResponse((item) => (
    new URL(item.url()).pathname === '/prompts/bindings' && item.request().method() === 'PUT'
  ));
  // 「新建 A/B 绑定」为多字按钮，antd insertSpace 不生效，普通 name 匹配。
  await page.getByRole('button', { name: '新建 A/B 绑定' }).click();
  expect((await savedResponse).status()).toBe(200);
  const rows = await withTenantQuery<{ stable_version_id: string; traffic_percent: string }>(pool, tenantID, {
    text: 'SELECT stable_version_id, traffic_percent::text AS traffic_percent FROM public.prompt_bindings WHERE key = $1 AND scope = $2',
    values: [key, `tenant:${tenantID}`],
  });
  expect(rows.rows).toEqual([{ stable_version_id: hash, traffic_percent: '0' }]);
  evidence.ui.push('A/B binding saved through drawer form');
  evidence.http.push('PUT /prompts/bindings returned 200');
  evidence.database.push('prompt_bindings row reflects saved scope and traffic_percent');
};

// prompt.delete.binding：清除 A/B 绑定，DELETE /prompts/bindings/:key/:scope
// 返回 200 后对账 prompt_bindings 该 key+scope 行消失。
const verifyDeleteBinding = async (
  page: Page, pool: DatabasePool, tenantID: string, key: string,
  evidence: EvidenceRecord,
) => {
  // 「清除」为两字按钮（insertSpace → 「清 除」），正则匹配。
  await page.getByRole('button', { name: /清\s*除/ }).click();
  const confirm = page.locator('.ant-popover:visible').filter({ hasText: '清除该 A/B 绑定？' });
  await expect(confirm).toBeVisible();
  const deletedResponse = page.waitForResponse((item) => (
    item.request().method() === 'DELETE'
    && new URL(item.url()).pathname.includes('/prompts/bindings/')
  ));
  // DangerPopconfirm 确认按钮沿用默认 okText「删除」（insertSpace → 「删 除」）。
  await confirm.getByRole('button', { name: /删\s*除/ }).click();
  expect((await deletedResponse).status()).toBe(200);
  const rows = await withTenantQuery<{ count: string }>(pool, tenantID, {
    text: 'SELECT count(*)::text AS count FROM public.prompt_bindings WHERE key = $1 AND scope = $2',
    values: [key, `tenant:${tenantID}`],
  });
  expect(rows.rows).toEqual([{ count: '0' }]);
  evidence.ui.push('A/B binding cleared through row confirmation');
  evidence.http.push('DELETE /prompts/bindings/:key/:scope returned 200');
  evidence.database.push('prompt_bindings row for the scope is gone');
};

export const executePromptPack = async ({ actor, pool, evidence, webURL }: PromptPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const page = await actor.context.newPage();
  try {
    await verifyRoute(page, pool, tenantID, evidence, webURL);
    const key = await verifyCreate(page, pool, tenantID, evidence);
    await verifyPublish(page, pool, tenantID, key, evidence);
    await verifyUpsertBinding(page, pool, tenantID, key, evidence);
    await verifyDeleteBinding(page, pool, tenantID, key, evidence);
    return [
      'prompt.route.prompts',
      'prompt.create.template',
      'prompt.publish.version',
      'prompt.upsert.binding',
      'prompt.delete.binding',
    ];
  } finally {
    await page.close();
  }
};
