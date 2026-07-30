import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import {
  assertCamelCaseKeys, assertNoPascalCase,
  MODEL_CAMEL_KEYS, MODEL_PASCAL_BANNED,
  PROVIDER_CAMEL_KEYS, PROVIDER_PASCAL_BANNED,
} from '../core/camelcase';
import { configureManagedModels, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface LLMAdminPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

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
// Ant Design uses "确 定" for OK button with zhCN locale
const clickModalOK = (page: Page) => page.locator('.ant-modal-content')
  .getByRole('button', { name: /确\s*定/ }).click();

export const executeLLMAdminPack = async ({ actor, pool, evidence, webURL }: LLMAdminPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const providerName = `E2E-Provider-${Date.now()}`;
  let providerID = '';
  try {
    // Clean up stale E2E providers and models from previous failed runs
    await withTenantQuery(pool, tenantID, {
      text: "DELETE FROM models WHERE provider_id IN (SELECT id FROM providers WHERE name LIKE 'E2E-%')",
      values: [],
    });
    await withTenantQuery(pool, tenantID, {
      text: "DELETE FROM providers WHERE name LIKE 'E2E-%'",
      values: [],
    });
    // Ensure baseline models are present (stateful-qwen provider + qwen models)
    await configureManagedModels(pool, tenantID);

    // ── Navigate to model management ──────────────────────────────────────────
    const listResponse = waitForMutation(page, '/admin/providers', 'GET');
    await page.goto(`${webURL}/models`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '模型管理' })).toBeVisible();
    completed.push('llm-admin.route.models');

    // ── Provider tab: create ──────────────────────────────────────────────────
    await page.getByRole('button', { name: '添加厂商' }).click();
    await page.getByLabel('名称').fill(providerName);
    // Select provider kind
    await page.locator('.ant-form-item').filter({ hasText: '类型' })
      .locator('.ant-select-selector').click();
    await page.locator('.ant-select-item-option').filter({ hasText: 'OpenAI 兼容' }).click();
    await page.getByLabel('Base URL').fill('http://127.0.0.1:19091/v1');
    await page.getByLabel('API Key').fill('sk-e2e-test-key');

    const createResponse = waitForMutation(page, '/admin/providers', 'POST');
    const createdListResponse = waitForMutation(page, '/admin/providers', 'GET');
    await clickModalOK(page);
    const created = await createResponse;
    expect(created.status()).toBe(201);
    const createdBody = await created.json();
    providerID = createdBody.id as string;

    // ── Guard: camelCase JSON keys in provider create response ────────────────
    assertCamelCaseKeys(createdBody, PROVIDER_CAMEL_KEYS, 'provider create');
    assertNoPascalCase(createdBody, PROVIDER_PASCAL_BANNED, 'provider create');
    expect(JSON.stringify(createdBody)).not.toContain('"apiKey"');

    expect((await createdListResponse).status()).toBe(200);
    expect(JSON.stringify(createdBody)).toContain(providerName);

    // Verify DB
    expect(await rows<{ name: string; enabled: boolean }>(pool, tenantID,
      'SELECT name, enabled FROM providers WHERE id=$1', [providerID]))
      .toEqual([{ name: providerName, enabled: true }]);
    completed.push('llm-admin.mutation.post.admin.providers');
    recordEvidence(evidence, 'LLM provider create');

    // ── Provider edit ─────────────────────────────────────────────────────────
    const providerRow = page.locator('tr').filter({ hasText: providerName });
    await providerRow.locator('button:has(.anticon-edit)').click();
    await expect(page.locator('.ant-modal-content').filter({ hasText: '编辑厂商' })).toBeVisible();
    await page.getByLabel('名称').fill(`${providerName}-edited`);
    await page.getByLabel('默认模型').fill('qwen-max');

    const updateResponse = waitForMutation(page, `/admin/providers/${providerID}`, 'PUT');
    const updatedListResponse = waitForMutation(page, '/admin/providers', 'GET');
    await clickModalOK(page);
    const updateData = await updateResponse;
    expect(updateData.status()).toBe(200);
    const updateBody = await updateData.json();
    assertCamelCaseKeys(updateBody, PROVIDER_CAMEL_KEYS, 'provider update');
    expect(JSON.stringify(updateBody)).toContain(`${providerName}-edited`);
    expect(JSON.stringify(updateBody)).toContain('qwen-max');

    expect((await updatedListResponse).status()).toBe(200);
    expect(await rows<{ name: string; default_model: string }>(pool, tenantID,
      'SELECT name, default_model FROM providers WHERE id=$1', [providerID]))
      .toEqual([{ name: `${providerName}-edited`, default_model: 'qwen-max' }]);
    completed.push('llm-admin.mutation.put.admin.providers.id');
    recordEvidence(evidence, 'LLM provider update');

    // ── Discover models from edited provider ──────────────────────────────────
    const editedRow = page.locator('tr').filter({ hasText: `${providerName}-edited` });
    await editedRow.getByRole('button', { name: '发现模型' }).click();

    const discoverResponse = waitForMutation(page, `/admin/providers/${providerID}/discover`, 'POST');
    await expect(page.locator('.ant-modal-content').filter({ hasText: '发现模型' })).toBeVisible();
    const discoverData = await discoverResponse;
    expect(discoverData.status()).toBe(200);
    const discoverBody = await discoverData.json();
    expect(discoverBody.models).toBeInstanceOf(Array);
    expect(discoverBody.count).toBeGreaterThan(0);

    // Guard: discovered models use camelCase
    assertCamelCaseKeys(discoverBody, ['providerManaged', 'providerId'], 'model discovery');
    assertNoPascalCase(discoverBody, ['ProviderManaged', 'ProviderID'], 'model discovery');

    // Close discover result modal
    await clickModalOK(page);

    completed.push('llm-admin.mutation.post.admin.providers.id.discover');
    recordEvidence(evidence, 'LLM model discovery');

    // ── Switch to model tab — guard camelCase in model list ───────────────────
    const modelsListResponse = waitForMutation(page, '/admin/models', 'GET');
    await page.locator('.ant-tabs-tab').filter({ hasText: '模型目录' }).click();
    const modelsList = await modelsListResponse;
    expect(modelsList.status()).toBe(200);
    const modelsBody = await modelsList.json();

    assertCamelCaseKeys(modelsBody, MODEL_CAMEL_KEYS, 'model list');
    assertNoPascalCase(modelsBody, MODEL_PASCAL_BANNED, 'model list');

    // Verify discovered models appear in model tab. Scope to the model card
    // to avoid matching provider-tab rows that share the same model names.
    const discoverModelNames = (discoverBody.models as Array<{ name: string }>).map(m => m.name);
    const firstModelName = discoverModelNames[0];
    const modelCard = page.locator('.ant-card').filter({ hasText: '模型目录' });
    // Scope model rows to the E2E provider — both stateful and E2E providers
    // can have models with the same name (e.g. qwen-max), and .first() would
    // pick the stateful one in DOM order, causing toggle+DB mismatch.
    const providerModelRow = (name: string) => modelCard.locator('tr')
      .filter({ hasText: name })
      .filter({ hasText: providerID })
      .first();
    if (firstModelName) {
      await expect(providerModelRow(firstModelName)).toBeVisible();
    }

    // ── Model edit via drawer ─────────────────────────────────────────────────
    if (firstModelName) {
      const modelRow = providerModelRow(firstModelName);
      await modelRow.locator('.ant-btn').first().click();
      await expect(page.locator('.ant-drawer')).toBeVisible();
      await expect(page.locator('.ant-drawer .ant-btn-primary')).toBeVisible();
      await expect(page.getByLabel('显示名称')).toBeVisible();

      await page.getByLabel('显示名称').fill(`E2E-${firstModelName}`);
      await page.getByLabel('输入价格 ($/1M tokens)').fill('1.23');
      await page.getByLabel('输出价格 ($/1M tokens)').fill('4.56');

      const modelUpdateResponse = waitForMutation(page, /\/admin\/models\/[^/]+$/, 'PUT');
      await page.locator('.ant-drawer .ant-btn-primary').click();
      const modelUpdated = await modelUpdateResponse;
      expect(modelUpdated.status()).toBe(200);
      const modelUpdateBody = await modelUpdated.json();
      assertCamelCaseKeys(modelUpdateBody, ['displayName', 'inputPrice', 'outputPrice'], 'model update');
      completed.push('llm-admin.mutation.put.admin.models.id');
      recordEvidence(evidence, 'LLM model update');

      // ── Model toggle ──────────────────────────────────────────────────────
      const updatedTableRow = modelCard.locator('tr')
        .filter({ hasText: `E2E-${firstModelName}` })
        .filter({ hasText: providerID })
        .first();
      const toggleSwitch = updatedTableRow.locator('.ant-switch');
      const ariaChecked = await toggleSwitch.getAttribute('aria-checked');
      const wasEnabled = ariaChecked === 'true';

      const toggleResponse = waitForMutation(page, /\/admin\/models\/[^/]+\/toggle/, 'PATCH');
      await toggleSwitch.click();
      expect((await toggleResponse).status()).toBe(200);

      expect(await rows<{ enabled: boolean }>(pool, tenantID,
        'SELECT enabled FROM models WHERE name=$1 AND provider_id=$2 AND tenant_id=$3',
        [firstModelName, providerID, tenantID]))
        .toEqual([{ enabled: !wasEnabled }]);

      // Toggle back
      const rowAfterToggle = modelCard.locator('tr')
        .filter({ hasText: `E2E-${firstModelName}` })
        .filter({ hasText: providerID })
        .first();
      const toggleBack = waitForMutation(page, /\/admin\/models\/[^/]+\/toggle/, 'PATCH');
      await rowAfterToggle.locator('.ant-switch').click();
      expect((await toggleBack).status()).toBe(200);
      completed.push('llm-admin.mutation.patch.admin.models.id.toggle');
      recordEvidence(evidence, 'LLM model toggle');
    }

    // ── Switch back to provider tab, delete provider ──────────────────────────
    await page.locator('.ant-tabs-tab').filter({ hasText: '厂商管理' }).click();

    const finalRow = page.locator('tr').filter({ hasText: `${providerName}-edited` });
    await finalRow.locator('button:has(.anticon-delete)').click();
    await expect(page.locator('.ant-modal-confirm')).toBeVisible();
    const deleteResponse = waitForMutation(page, `/admin/providers/${providerID}`, 'DELETE');
    await page.locator('.ant-modal-confirm .ant-btn-dangerous').click();
    expect((await deleteResponse).status()).toBe(200);

    expect(await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM providers WHERE id=$1', [providerID]))
      .toEqual([{ count: '0' }]);
    completed.push('llm-admin.mutation.delete.admin.providers.id');
    recordEvidence(evidence, 'LLM provider deletion');
  } finally {
    await page.close();
  }
  return completed;
};
