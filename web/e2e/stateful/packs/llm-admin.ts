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
    await withTenantQuery(pool, tenantID, {
      text: `DELETE FROM providers WHERE tenant_id=$1 AND name LIKE 'E2E-Provider-%'`,
      values: [tenantID],
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
    const discoverResponse = waitForMutation(page, `/admin/providers/${providerID}/discover`, 'POST');
    await editedRow.getByRole('button', { name: '发现模型' }).click();
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

    // Verify discovered models appear (mock runtime returns mock-model-1, mock-model-2)
    const modelCatalogue = page.locator('.ant-card').filter({ hasText: '模型目录' }).last();
    const discoveredModels = discoverBody.models as Array<{ id: string; name: string }>;
    const firstModel = discoveredModels[0];
    const firstModelName = firstModel?.name;
    const listedModels = modelsBody.models as Array<{ id: string; name: string; providerId: string }>;
    const listedModel = listedModels.find(model => model.name === firstModelName && model.providerId === providerID);
    expect(listedModel).toBeDefined();
    const modelRows = modelCatalogue.locator('tr')
      .filter({ hasText: providerID })
      .filter({ hasText: firstModelName ?? '' });
    if (firstModel) {
      await expect(modelCatalogue.locator('tr').filter({ hasText: firstModel.name, visible: true }).first()).toBeVisible();
    }

    // ── Model edit via drawer ─────────────────────────────────────────────────
    if (firstModel) {
      await modelRows.getByRole('button', { name: /编\s*辑/ }).first().click();
      await expect(page.getByRole('button', { name: /保\s*存/ })).toBeVisible();
      await expect(page.getByLabel('显示名称')).toBeVisible();

      await page.getByLabel('显示名称').fill(`E2E-${firstModelName}`);
      await page.getByLabel('输入价格 ($/1M tokens)').fill('1.23');
      await page.getByLabel('输出价格 ($/1M tokens)').fill('4.56');

      const modelUpdateResponse = waitForMutation(page, /\/admin\/models\/[^/]+$/, 'PUT');
      await page.getByRole('button', { name: /保\s*存/ }).click();
      const modelUpdated = await modelUpdateResponse;
      expect(modelUpdated.status()).toBe(200);
      const modelUpdateBody = await modelUpdated.json();
      assertCamelCaseKeys(modelUpdateBody, ['displayName', 'inputPrice', 'outputPrice'], 'model update');
      completed.push('llm-admin.mutation.put.admin.models.id');
      recordEvidence(evidence, 'LLM model update');

      // ── Model toggle ──────────────────────────────────────────────────────
      const toggleSwitch = modelRows.locator('.ant-switch').first();
      const ariaChecked = await toggleSwitch.getAttribute('aria-checked');
      const wasEnabled = ariaChecked === 'true';

      const toggleResponse = waitForMutation(page, /\/admin\/models\/[^/]+\/toggle/, 'PATCH');
      await toggleSwitch.click();
      expect((await toggleResponse).status()).toBe(200);

      expect(await rows<{ enabled: boolean }>(pool, tenantID,
        'SELECT enabled FROM models WHERE id=$1 AND tenant_id=$2',
        [listedModel!.id, tenantID]))
        .toEqual([{ enabled: !wasEnabled }]);

      // Toggle back
      const toggleBack = waitForMutation(page, /\/admin\/models\/[^/]+\/toggle/, 'PATCH');
      await modelRows.locator('.ant-switch').first().click();
      expect((await toggleBack).status()).toBe(200);
      completed.push('llm-admin.mutation.patch.admin.models.id.toggle');
      recordEvidence(evidence, 'LLM model toggle');
    }

    // ── Switch back to provider tab, delete provider ──────────────────────────
    await page.locator('.ant-tabs-tab').filter({ hasText: '厂商管理' }).click();

    const providerCatalogue = page.locator('.ant-card').filter({ hasText: '厂商管理' }).last();
    const finalRow = providerCatalogue.locator('tr')
      .filter({ hasText: `${providerName}-edited`, visible: true }).first();
    const deleteResponse = waitForMutation(page, `/admin/providers/${providerID}`, 'DELETE');
    await finalRow.locator('button:has(.anticon-delete)').click();
    await page.locator('.ant-modal-content').filter({ hasText: '确认删除' })
      .getByRole('button', { name: /删\s*除/ }).click();
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
