import { expect, test, type Page, type Route } from '@playwright/test';

const MOBILE_BREAKPOINT = 768;

const agents = [
  {
    id: 'agent-1',
    name: '客服助手',
    description: '回答产品和交付问题',
    type: 'react',
    llmModel: 'gpt-4o-mini',
    allowedSkills: [],
    mcpServerIds: [],
    knowledgeWorkspaceIds: [],
  },
];

const executions = [
  {
    id: 'execution-1',
    agent_name: '客服助手',
    status: 'success',
    input_preview: '查询移动端适配进度',
    output_preview: '适配已完成',
    total_tokens: 128,
    duration_ms: 860,
    created_at: '2026-07-14T08:00:00Z',
  },
];

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

async function mockAuthenticatedApi(page: Page) {
  await page.route('**/auth/refresh', (route) => json(route, { access_token: 'e2e-token' }));
  await page.route('**/auth/me', (route) =>
    json(route, {
      sub: 'e2e-user',
      tenant_id: 'tenant-1',
      role: 'admin',
      github_login: 'responsive-user',
    }),
  );
  await page.route('**/api/v1/tenant/settings', (route) =>
    json(route, { tenant_id: 'tenant-1', tenant_name: '移动端验收租户', settings: {} }),
  );
  await page.route('**/tenant/list', (route) =>
    json(route, { tenants: [{ tenant_id: 'tenant-1', name: '移动端验收租户' }] }),
  );
  await page.route('**/health', (route) => json(route, { status: 'ok' }));
  await page.route('**/agents/executions**', (route) =>
    json(route, { executions, total: executions.length }),
  );
  await page.route('**/agents/agent-1/conversations', (route) =>
    json(route, {
      conversations: [
        { id: 'conversation-1', agent_id: 'agent-1', name: '响应式验收会话' },
      ],
    }),
  );
  await page.route('**/conversations/conversation-1/messages', (route) =>
    json(route, {
      messages: [
        { id: 'message-1', role: 'assistant', content: '欢迎使用移动端对话。' },
      ],
    }),
  );
  await page.route('**/agents', (route) => json(route, { agents }));
  await page.route('**/skills', (route) => json(route, { skills: [] }));
  await page.route('**/mcp/servers', (route) => json(route, { servers: [] }));
  await page.route('**/knowledge/workspaces', (route) =>
    json(route, {
      workspaces: [
        {
          name: 'product-docs',
          description: '产品文档与常见问题',
          embedding_model: 'text-embedding-v3',
          document_count: 3,
        },
      ],
    }),
  );
}

async function expectNoHorizontalOverflow(page: Page) {
  const dimensions = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }));
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.innerWidth + 1);
}

async function expectWithinViewport(page: Page, selector: string) {
  const locator = page.locator(selector).filter({ visible: true }).first();
  await expect(locator).toBeVisible();
  const box = await locator.boundingBox();
  const viewport = page.viewportSize();
  expect(box).not.toBeNull();
  expect(viewport).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(-1);
  expect(box!.y).toBeGreaterThanOrEqual(-1);
  expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1);
  expect(box!.y + box!.height).toBeLessThanOrEqual(viewport!.height + 1);
}

async function expectWithinHorizontalViewport(page: Page, selector: string) {
  const locator = page.locator(selector).filter({ visible: true }).first();
  await expect(locator).toBeVisible();
  const box = await locator.boundingBox();
  const viewport = page.viewportSize();
  expect(box).not.toBeNull();
  expect(viewport).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(-1);
  expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1);
}

test('login remains usable without horizontal overflow', async ({ page }) => {
  await page.route('**/auth/refresh', (route) => json(route, { error: 'unauthorized' }, 401));
  await page.goto('/login');

  await expect(page.getByRole('heading', { name: 'Stratum AI' })).toBeVisible();
  await expect(page.getByRole('button', { name: /使用 GitHub 登录/ })).toBeVisible();
  await expectWithinViewport(page, '.auth-card');
  await expectNoHorizontalOverflow(page);
});

test.describe('authenticated responsive workflows', () => {
  test.beforeEach(async ({ page }) => {
    await mockAuthenticatedApi(page);
  });

  test('shell navigation and dashboard data adapt to the viewport', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();

    const isMobile = page.viewportSize()!.width < MOBILE_BREAKPOINT;
    if (isMobile) {
      await expect(page.locator('.ant-table')).toHaveCount(0);
      await expect(page.getByText('查询移动端适配进度')).toBeVisible();

      await page.getByRole('button', { name: '打开主导航' }).click();
      const navigation = page.getByRole('navigation', { name: '主导航' });
      await expect(navigation).toBeVisible();
      await navigation.getByText('知识库', { exact: true }).click();
      await expect(page).toHaveURL(/\/knowledge$/);
      await expect(navigation).not.toBeVisible();
      await expect(page.getByText('产品文档与常见问题')).toBeVisible();
    } else {
      await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible();
      await expect(page.locator('.ant-table')).toBeVisible();
      await expect(page.getByRole('button', { name: '打开主导航' })).toHaveCount(0);
    }

    await expectNoHorizontalOverflow(page);
  });

  test('knowledge creation modal stays inside the viewport', async ({ page }) => {
    await page.goto('/knowledge');
    await page.getByRole('button', { name: /新建知识库/ }).click();

    await expect(page.getByRole('dialog', { name: '新建知识库' })).toBeVisible();
    await expectWithinViewport(page, '.ant-modal');
    await expectNoHorizontalOverflow(page);
  });

  test('tenant settings embedding controls stay inside the viewport', async ({ page }) => {
    await page.goto('/tenant/settings');

    await expect(page.getByRole('heading', { name: '租户设置' })).toBeVisible();
    await expectWithinHorizontalViewport(page, '.tenant-embedding-card');
    await expectWithinHorizontalViewport(page, '.tenant-embedding-card .ant-select');
    await expectNoHorizontalOverflow(page);
  });

  test('chat keeps conversation navigation and composer usable', async ({ page }) => {
    await page.goto('/chat');
    await expect(page.getByText('请选择 Agent')).toBeVisible();

    const isMobile = page.viewportSize()!.width < MOBILE_BREAKPOINT;
    if (isMobile) {
      await page.getByRole('button', { name: '打开会话列表' }).click();
      const drawer = page.getByRole('dialog', { name: '会话列表' });
      await expect(drawer).toBeVisible();
      await drawer.locator('.ant-select').click();
      await page.getByText('客服助手', { exact: true }).last().click();
      await drawer.getByText('响应式验收会话').click();
      await expect(drawer).not.toBeVisible();
    } else {
      await page.locator('.agent-chat-page .ant-select').click();
      await page.getByText('客服助手', { exact: true }).last().click();
      await page.getByText('响应式验收会话').click();
    }

    const composer = page.locator('.chat-composer');
    await expect(composer.getByPlaceholder(/输入消息/)).toBeEnabled();
    await expectWithinViewport(page, '.chat-composer');
    await expectNoHorizontalOverflow(page);
  });
});
