import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import {
  addGeneratedActorMembership, requireUUID, withTenantMutation, withTenantQuery, type DatabasePool,
} from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { runCleanupTasks } from '../core/errors';

interface MechanismPackContext {
  systemAdmin: BrowserActor;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  backendURL: string;
}

const waitForMutation = (page: Page, path: string | RegExp, method: string) => page.waitForResponse((response) => {
  const urlPath = new URL(response.url()).pathname;
  const pathMatch = typeof path === 'string' ? urlPath === path : path.test(urlPath);
  return pathMatch && response.request().method() === method;
});

const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;

const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};

// 机制管理面依附默认租户：真实默认租户 id 是 UUID（tenants.id DEFAULT
// uuid_generate_v4），必须以 public.tenants.is_default 解析，不能假设字面值。
const resolveDefaultTenantID = async (pool: DatabasePool): Promise<string> => {
  const client = await pool.connect();
  try {
    const result = await client.query<{ id: string }>(
      'SELECT id FROM public.tenants WHERE is_default = true AND deleted_at IS NULL LIMIT 1',
      [],
    );
    if (result.rowCount !== 1) throw new Error('default tenant could not be resolved');
    return requireUUID(result.rows[0].id, 'default_tenant_id');
  } finally {
    client.release();
  }
};

// switchToTenant 走真实 /auth/switch-tenant，把 systemAdmin 的浏览器会话切到
// 默认租户（refresh cookie 一并更新），返回替换后的 access token。不覆盖
// actor.accessToken：下一 pack 前 restoreActorSession 仍需原会话切回。
const switchToTenant = async (actor: BrowserActor, backendURL: string, tenantID: string): Promise<string> => {
  const response = await actor.context.request.post(`${backendURL}/auth/switch-tenant`, {
    data: { tenant_id: tenantID },
    headers: { Authorization: `Bearer ${actor.accessToken}` },
  });
  if (response.status() !== 200) {
    throw new Error(`switch-tenant to default tenant failed with status ${response.status()}`);
  }
  const body = await response.json() as { access_token?: unknown };
  if (typeof body.access_token !== 'string' || body.access_token.length === 0) {
    throw new Error('switch-tenant returned no access token');
  }
  return body.access_token;
};

export const executeMechanismPack = async ({
  systemAdmin, pool, evidence, webURL, backendURL,
}: MechanismPackContext): Promise<string[]> => {
  const userID = requireUUID(systemAdmin.userID ?? '', 'system_admin user_id');
  const defaultTenantID = await resolveDefaultTenantID(pool);
  await addGeneratedActorMembership(pool, defaultTenantID, userID, 'admin');
  await switchToTenant(systemAdmin, backendURL, defaultTenantID);

  const page = await systemAdmin.context.newPage();
  const familyKey = `e2e-mech-${Date.now()}`;
  const completed: string[] = [];
  try {
    // ── Profiles tab: route + upsert（建档 draft）────────────────────
    const listResponse = waitForMutation(page, '/mechanism/profiles', 'GET');
    await page.goto(`${webURL}/mechanism/profiles`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '模型档案' })).toBeVisible();
    completed.push('mechanism.route.profiles');

    await page.getByRole('button', { name: '新建档案' }).click();
    const drawer = page.locator('.ant-drawer:visible');
    await expect(drawer).toBeVisible();
    await page.getByLabel('档案名称').fill(`E2E 机制档案 ${Date.now()}`);
    // 家族前缀 Select 是 mode="tags" open={false}：搜索 input 不参与可访问性树
    // 且下拉永不开，getByLabel 定位不到控件内部。改用现有 pack 的 form-item
    // filter 模式，模拟真实用户：点击 selector 聚焦搜索框，键盘输入后 Enter 添加。
    const prefixes = page.locator('.ant-form-item').filter({ hasText: '家族前缀' }).locator('.ant-select');
    await prefixes.locator('.ant-select-selector').click();
    await page.keyboard.type(familyKey);
    await page.keyboard.press('Enter');
    await expect(prefixes.locator('.ant-select-selection-item')).toContainText(familyKey);
    await page.getByLabel('记忆提取模板').fill('抽取 {input} 中的关键事实并以 JSON 输出。');
    await page.getByLabel('记忆富化模型').fill('qwen-max');
    await page.getByLabel('记忆总结模型').fill('qwen-max');

    const upsertResponse = waitForMutation(page, '/mechanism/profiles', 'PUT');
    await page.getByRole('button', { name: '保存档案' }).click();
    const upserted = await upsertResponse;
    expect(upserted.status()).toBe(200);
    const upsertBody = await upserted.json() as { family_key: string; fingerprint: string; version: number };
    expect(upsertBody.family_key).toBe(familyKey);
    expect(upsertBody.fingerprint).toBeTruthy();
    expect(upsertBody.version).toBeGreaterThanOrEqual(1);
    await expect(drawer).toBeHidden();

    expect(await rows<{ status: string; version: number }>(pool, defaultTenantID,
      'SELECT status, version FROM public.model_profiles WHERE family_key=$1', [familyKey]))
      .toEqual([{ status: 'draft', version: 1 }]);
    completed.push('mechanism.mutation.upsert.profiles');
    recordEvidence(evidence, 'Mechanism profile upsert');

    // ── Matrix tab: report → run → adopt ─────────────────────────────
    const matrixResponse = waitForMutation(page, '/mechanism/matrix', 'GET');
    await page.getByRole('tab', { name: '评测矩阵' }).click();
    expect((await matrixResponse).status()).toBe(200);
    await expect(page.getByRole('cell', { name: familyKey })).toBeVisible();

    const runResponse = waitForMutation(page, '/mechanism/matrix/runs', 'POST');
    await page.getByRole('button', { name: '触发评测' }).click();
    await page.locator('.ant-modal-content').getByRole('button', { name: /触\s*发\s*评\s*测/ }).click();
    const run = await runResponse;
    expect(run.status()).toBe(200);
    const runBody = await run.json() as { suite_revision_id: string; triggered_count: number };
    expect(runBody.suite_revision_id).toBeTruthy();
    expect(runBody.triggered_count).toBeGreaterThanOrEqual(1);

    expect(Number((await rows<{ count: string }>(pool, defaultTenantID, `
      SELECT count(*)::text AS count FROM evaluation_jobs
      WHERE idempotency_key LIKE 'matrix:' || $1 || ':%'`, [familyKey]))[0]?.count))
      .toBeGreaterThanOrEqual(1);
    completed.push('mechanism.mutation.run.matrix');
    recordEvidence(evidence, 'Mechanism matrix run');

    const adoptResponse = waitForMutation(page, '/mechanism/matrix/adopt', 'POST');
    await page.getByRole('row').filter({ hasText: familyKey }).getByRole('button', { name: '采纳' }).click();
    await page.locator('.ant-modal-content').getByRole('button', { name: /采\s*纳/ }).click();
    const adopted = await adoptResponse;
    expect(adopted.status()).toBe(200);
    const adoptBody = await adopted.json() as { family_key: string; status: string };
    expect(adoptBody.family_key).toBe(familyKey);
    expect(adoptBody.status).toBe('active');

    expect(await rows<{ status: string; version: number }>(pool, defaultTenantID,
      'SELECT status, version FROM public.model_profiles WHERE family_key=$1', [familyKey]))
      .toEqual([{ status: 'active', version: 2 }]);
    completed.push('mechanism.mutation.adopt.matrix');
    recordEvidence(evidence, 'Mechanism matrix adopt');
  } finally {
    const cleanupTasks: Array<() => Promise<unknown>> = [
      async () => withTenantMutation(pool, defaultTenantID, {
        text: 'DELETE FROM public.model_profiles WHERE family_key=$1', values: [familyKey],
      }),
      async () => withTenantMutation(pool, defaultTenantID, {
        text: "DELETE FROM evaluation_jobs WHERE idempotency_key LIKE 'matrix:' || $1 || ':%'", values: [familyKey],
      }),
      async () => withTenantMutation(pool, defaultTenantID, {
        text: 'DELETE FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2', values: [defaultTenantID, userID],
      }),
      async () => page.close(),
    ];
    await runCleanupTasks(cleanupTasks);
  }
  return completed;
};
