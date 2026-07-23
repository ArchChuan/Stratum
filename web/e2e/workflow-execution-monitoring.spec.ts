import { expect, test } from '@playwright/test';

import { createPublishedApprovalWorkflow, createRealSession, queryTenant } from './support/real-workflow';

test.skip(process.env.REAL_E2E !== '1', 'set REAL_E2E=1 to run against the local backend and database');

test('mobile member navigates to a published workflow, starts it, and sees ordered run steps', async ({ context, page }) => {
  test.skip(page.viewportSize()!.width >= 768, 'this scenario verifies the mobile navigation and ordered step view');
  const session = await createRealSession(context, 'member');
  const workflow = await createPublishedApprovalWorkflow();
  await page.goto('/workflows');
  await page.getByRole('button', { name: '打开主导航' }).click();
  const navigation = page.getByRole('navigation', { name: '主导航' });
  await navigation.getByText('工作流', { exact: true }).click();
  await expect(navigation).not.toBeVisible();

  const workflowCard = page.locator('.workflow-catalog-card').filter({ hasText: workflow.name });
  await expect(workflowCard).toBeVisible();
  await workflowCard.getByRole('button', { name: '运行工作流' }).click();
  await page.getByLabel('审批事项').fill('移动端真实运行');
  const startResponse = page.waitForResponse((response) =>
    response.url().endsWith('/workflow-runs') && response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: '开始运行' }).click();
  const started = await startResponse;
  expect([200, 201, 202]).toContain(started.status());
  const run = await started.json() as { run_id: string };
  await expect(page).toHaveURL(new RegExp(`/workflow-runs/${run.run_id}$`));
  await expect(page.getByText('管理员审批')).toBeVisible();
  await expect(page.getByRole('region', { name: '工作流运行图' })).toHaveCount(0);

  await expect.poll(() => queryTenant(session.tenantId,
    `SELECT status || '|' || created_by FROM workflow_runs WHERE id='${run.run_id}'`,
  )).toMatch(/^(paused|running)\|.+/);
});
