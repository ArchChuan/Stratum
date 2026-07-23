import { expect, test } from '@playwright/test';

import { createRealSession, queryTenant } from './support/real-workflow';
import { workflowDefinitionSchema } from '../src/modules/workflow/model/workflow';

test.skip(process.env.REAL_E2E !== '1', 'set REAL_E2E=1 to run against the local backend and database');

test('admin creates, validates, publishes, and reads back an approval workflow', async ({ context, page }) => {
  test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
  const session = await createRealSession(context, 'admin');
  const workflowName = `E2E-审批流程-${Date.now()}`;

  await page.goto('/workflows/new');
  await expect(page.getByRole('region', { name: '工作流画布' })).toBeVisible();
  await page.getByLabel('工作流名称').fill(workflowName);
  await page.getByLabel('任务名称').fill('审批事项');
  await page.getByRole('button', { name: '添加人工审批节点' }).click();
  await page.getByRole('textbox', { name: '节点名称', exact: true }).fill('管理员审批');

  const createResponse = page.waitForResponse((response) =>
    response.url().endsWith('/workflows') && response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: '保存草稿' }).click();
  const created = await createResponse;
  expect(created.status()).toBe(201);
  const definition = await created.json() as { id: string; revision: number };
  workflowDefinitionSchema.parse(definition);
  await expect(page).toHaveURL(new RegExp(`/workflows/${definition.id}/edit$`));

  const validateResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/workflows/${definition.id}/validate`),
  );
  await page.getByRole('button', { name: '校验工作流' }).click();
  expect((await validateResponse).status()).toBe(200);
  await expect(page.getByText('校验通过', { exact: true })).toBeVisible();

  const publishResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/workflows/${definition.id}/publish`),
  );
  await page.getByRole('button', { name: '发布工作流' }).click();
  await page.getByRole('button', { name: '确认发布' }).click();
  const published = await publishResponse;
  expect(published.status()).toBe(201);
  const version = await published.json() as { id: string; version: number };
  await expect(page.getByRole('region', { name: '工作流版本图' })).toBeVisible();
  await page.reload();
  await expect(page.getByText(workflowName)).toBeVisible();

  expect(queryTenant(session.tenantId,
    `SELECT count(*) FROM workflow_definitions WHERE id='${definition.id}' AND draft_revision=${definition.revision}`,
  )).toContain('1');
  expect(queryTenant(session.tenantId,
    `SELECT count(*) FROM workflow_versions WHERE id='${version.id}' AND definition_id='${definition.id}' AND version_no=${version.version}`,
  )).toContain('1');
});

test('desktop palette exposes all five workflow node types', async ({ context, page }) => {
  test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
  await createRealSession(context, 'admin');
  await page.goto('/workflows/new');

  for (const label of ['Agent', 'Skill', 'MCP 工具', '条件判断', '人工审批']) {
    await page.getByRole('button', { name: `添加${label}节点` }).click();
  }
  await expect(page.locator('.react-flow__node')).toHaveCount(5);
});
