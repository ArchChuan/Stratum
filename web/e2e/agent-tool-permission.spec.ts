import { expect, test, type Page, type Route } from '@playwright/test';

type ApprovalStatus =
  | 'pending'
  | 'expired'
  | 'unknown_outcome'
  | 'authorization_denied';

interface ApprovalFixture {
  id: string;
  agent_id: string;
  tool_name: string;
  server_id: string;
  risk_level: string;
  status: ApprovalStatus;
  expires_at?: string;
}

interface HarnessOptions {
  role?: 'admin' | 'member';
  approvals?: ApprovalFixture[];
  resumeStatus?: number;
  resumeError?: string;
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

async function installHarness(page: Page, options: HarnessOptions = {}) {
  const role = options.role ?? 'admin';
  const approvals = [...(options.approvals ?? [])];
  const calls = { decisions: 0, resumes: 0, streams: 0 };

  await page.route('**/auth/refresh', (route) => json(route, { access_token: 'e2e-token' }));
  await page.route('**/auth/me', (route) =>
    json(route, { sub: 'user-1', tenant_id: 'tenant-1', role }),
  );
  await page.route('**/api/v1/tenant/settings', (route) =>
    json(route, { tenant_id: 'tenant-1', tenant_name: '权限验收租户', settings: {} }),
  );
  await page.route('**/tenant/list', (route) =>
    json(route, { tenants: [{ tenant_id: 'tenant-1', name: '权限验收租户' }] }),
  );
  await page.route('**/agents/tool-approvals', (route) =>
    json(route, { approvals }),
  );
  await page.route('**/agents/tool-approvals/*/decision', async (route) => {
    calls.decisions += 1;
    const body = route.request().postDataJSON() as { decision: 'approved' | 'rejected' };
    if (body.decision === 'rejected') approvals.splice(0, approvals.length);
    await json(route, { status: body.decision });
  });
  await page.route('**/agents/tool-approvals/*/resume', async (route) => {
    calls.resumes += 1;
    if (options.resumeStatus) {
      await json(route, { error: options.resumeError }, options.resumeStatus);
      return;
    }
    approvals.splice(0, approvals.length);
    await json(route, { status: 'completed', output: '删除操作已完成', steps: 1, tokensUsed: 8 });
  });
  await page.route('**/agents/agent-1/execute/stream', async (route) => {
    calls.streams += 1;
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: 'event: done\ndata: {"done":true,"output":"读取完成","steps":[]}\n\n',
    });
  });
  await page.route('**/agents/agent-1/conversations', (route) =>
    json(route, { conversations: [{ id: 'conversation-1', agent_id: 'agent-1', name: '权限会话' }] }),
  );
  await page.route('**/conversations/conversation-1/messages', (route) =>
    json(route, { messages: [] }),
  );
  await page.route('**/agents', (route) =>
    json(route, {
      agents: [{
        id: 'agent-1',
        name: '权限 Agent',
        description: '工具权限验收',
        llmModel: 'deterministic-stub',
        allowedSkills: [],
        mcpToolIds: ['orders.delete'],
        knowledgeWorkspaceIds: [],
      }],
    }),
  );

  return calls;
}

async function openChat(page: Page) {
  await page.goto('/chat');
  const mobile = page.viewportSize()!.width < 768;
  if (mobile) {
    await page.getByRole('button', { name: '打开会话列表' }).click();
    const drawer = page.getByRole('dialog', { name: '会话列表' });
    await drawer.locator('.ant-select').click();
    await page.getByText('权限 Agent', { exact: true }).last().click();
    await drawer.getByText('权限会话').click();
  } else {
    await page.locator('.agent-chat-page .ant-select').click();
    await page.getByText('权限 Agent', { exact: true }).last().click();
    await page.getByText('权限会话').click();
  }
  await expect(page.getByPlaceholder(/输入消息/)).toBeEnabled();
}

const pendingApproval = (status: ApprovalStatus = 'pending'): ApprovalFixture => ({
  id: 'approval-1',
  agent_id: 'agent-1',
  tool_name: 'delete_order',
  server_id: 'orders',
  risk_level: 'destructive',
  status,
});

test('read-only tool execution completes without approval', async ({ page }) => {
  const calls = await installHarness(page);
  await openChat(page);
  await page.getByPlaceholder(/输入消息/).fill('读取订单');
  await page.getByRole('button', { name: '发送消息' }).click();

  await expect(page.getByText('读取完成')).toBeVisible();
  expect(calls.streams).toBe(1);
  expect(calls.decisions).toBe(0);
  expect(calls.resumes).toBe(0);
});

test('destructive tool pauses for admin approval and executes exactly once', async ({ page }) => {
  const calls = await installHarness(page, { approvals: [pendingApproval()] });
  await openChat(page);

  const approve = page.getByRole('button', { name: '批准并继续' });
  await expect(approve).toBeVisible();
  await approve.dblclick();

  await expect(page.getByText('删除操作已完成')).toBeVisible();
  expect(calls.decisions).toBe(1);
  expect(calls.resumes).toBe(1);
});

test('admin can reject a destructive tool without executing it', async ({ page }) => {
  const calls = await installHarness(page, { approvals: [pendingApproval()] });
  await openChat(page);
  await page.getByRole('button', { name: '拒绝' }).click();

  await expect(page.getByText('工具 delete_order 等待审批')).not.toBeVisible();
  expect(calls.decisions).toBe(1);
  expect(calls.resumes).toBe(0);
});

for (const [status, text] of [
  ['expired', '工具审批已过期'],
  ['authorization_denied', '权限已变更，工具执行已阻止'],
  ['unknown_outcome', '工具执行结果未知，需要人工对账'],
] as const) {
  test(`${status} is a terminal state without retry actions`, async ({ page }) => {
    const calls = await installHarness(page, { approvals: [pendingApproval(status)] });
    await openChat(page);

    await expect(page.getByText(text)).toBeVisible();
    await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
    expect(calls.decisions).toBe(0);
    expect(calls.resumes).toBe(0);
  });
}

test('unknown outcome returned by resume becomes reconciliation work', async ({ page }) => {
  const calls = await installHarness(page, {
    approvals: [pendingApproval()],
    resumeStatus: 409,
    resumeError: 'tool approval outcome is unknown',
  });
  await openChat(page);
  await page.getByRole('button', { name: '批准并继续' }).click();

  await expect(page.locator('.ant-alert-message').getByText('工具执行结果未知，需要人工对账')).toBeVisible();
  await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
  expect(calls.decisions).toBe(1);
  expect(calls.resumes).toBe(1);
});

test('member sees only own-tenant approval explanation and no commands', async ({ page }) => {
  const calls = await installHarness(page, { role: 'member', approvals: [pendingApproval()] });
  await openChat(page);

  await expect(page.getByText('需要租户管理员处理')).toBeVisible();
  await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
  await expect(page.getByText('cross-tenant-secret')).toHaveCount(0);
  expect(calls.decisions).toBe(0);
  expect(calls.resumes).toBe(0);
});
