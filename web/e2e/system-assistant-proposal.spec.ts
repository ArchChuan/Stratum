import { expect, test, type Page, type Route } from '@playwright/test';

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const baseProposal = {
  id: 'proposal-browser', proposerId: 'admin-browser', resourceKind: 'knowledge_workspace',
  operation: 'create', summary: 'create knowledge_workspace', status: 'ready_for_review',
  payload: { name: '官方文档', description: '已核验产品资料', embeddingModel: 'text-embedding-v3' },
  events: [{ id: 'event-1', actorId: 'admin-browser', toStatus: 'ready_for_review', createdAt: '2026-07-27T00:00:00Z' }],
  expiresAt: '2026-07-28T00:00:00Z', createdAt: '2026-07-27T00:00:00Z', updatedAt: '2026-07-27T00:00:00Z',
};

async function installAuth(page: Page, role: 'member' | 'admin') {
  await page.route('**/auth/refresh', (route) => json(route, { access_token: 'browser-fixture-token' }));
  await page.route('**/auth/me', (route) => json(route, { sub: `${role}-browser`, tenant_id: 'tenant-browser', role }));
  await page.route('**/api/v1/tenant/settings', (route) => json(route, { tenant_id: 'tenant-browser', tenant_name: '提案验收租户' }));
  await page.route('**/tenant/list', (route) => json(route, { tenants: [{ tenant_id: 'tenant-browser', name: '提案验收租户' }] }));
}

test.beforeEach(async ({ page }, testInfo) => {
  test.skip(!['mobile-390', 'desktop-1440'].includes(testInfo.project.name), 'proposal acceptance viewports');
});

test('admin reviews, edits, and confirms exactly once without overflow', async ({ page }) => {
  await installAuth(page, 'admin');
  let proposal = structuredClone(baseProposal);
  let confirmCalls = 0;
  await page.route('**/resource-change-proposals/proposal-browser', async (route) => {
    if (route.request().resourceType() === 'document') {
      await route.continue();
      return;
    }
    if (route.request().method() === 'PATCH') {
      const body = route.request().postDataJSON() as { payload: typeof baseProposal.payload };
      proposal = { ...proposal, payload: body.payload };
    }
    await json(route, proposal);
  });
  await page.route('**/resource-change-proposals/proposal-browser/confirm', (route) => {
    confirmCalls += 1;
    proposal = { ...proposal, status: 'applied', applyResult: { resourceId: 'workspace-browser' } };
    return json(route, proposal);
  });
  await page.route('**/resource-change-proposals/proposal-browser/cancel', (route) => json(route, { status: 'cancelled' }));

  await page.goto('/resource-change-proposals/proposal-browser');
  await expect(page.getByRole('heading', { name: '审阅资源变更' })).toBeVisible();
  await expect(page.getByRole('row', { name: /说明 已核验产品资料/ })).toBeVisible();
  await page.getByRole('textbox', { name: '说明' }).fill('已核验产品资料与变更记录');
  await page.getByRole('button', { name: '保存调整' }).click();
  await expect(page.getByText('提案已保存')).toBeVisible();

  await page.getByRole('button', { name: '确认并应用' }).click();
  const dialog = page.locator('.ant-modal:visible');
  await expect(dialog.getByText('确认应用这次变更？').last()).toBeVisible();
  await dialog.getByRole('button', { name: '确认并应用' }).click();
  await expect(page.getByText('已应用').first()).toBeVisible();
  expect(confirmCalls).toBe(1);
  await expect(page.getByRole('button', { name: '确认并应用' })).toHaveCount(0);

  const overflow = await page.locator('body').evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
  expect(overflow).toBe(false);
});

test('member cannot open the proposal review route', async ({ page }) => {
  await installAuth(page, 'member');
  await page.goto('/resource-change-proposals/proposal-browser');
  await expect(page.getByText('仅管理员可访问此页面，普通成员无权限。')).toBeVisible();
  await expect(page.getByRole('button', { name: '确认并应用' })).toHaveCount(0);
});
