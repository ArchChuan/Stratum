import { expect, test, type Page, type Route } from '@playwright/test';

type ApprovalStatus =
  | 'pending'
  | 'approved'
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
  conversation_id?: string;
  expires_at?: string;
}

interface HarnessOptions {
  role?: 'admin' | 'member';
  approvals?: ApprovalFixture[];
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

// M3/M4:对话页审批卡片只读契约。审批操作(批准/拒绝/执行)收敛到审批中心(/approvals),
// 对话页对 admin/member 一律只读提示;approved 态提供"继续执行"手动兜底(自动续跑走轮询)。
async function installHarness(page: Page, options: HarnessOptions = {}) {
  const role = options.role ?? 'admin';
  const approvals = [...(options.approvals ?? [])];
  const calls = { streams: 0 };

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
  // 审批等待卡片轮询 active-execution:route-mock 下无真实后端,统一返回 404(none),
  // 避免把无执行误判为续跑目标;非 404 错误(DB 抖动)会抛出让测试暴露(SECURITY-MEDIUM-1)。
  await page.route('**/conversations/conversation-1/active-execution', (route) =>
    json(route, { status: 'none' }, 404),
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
  conversation_id: 'conversation-1',
});

test('read-only tool execution completes without approval', async ({ page }) => {
  const calls = await installHarness(page);
  await openChat(page);
  await page.getByPlaceholder(/输入消息/).fill('读取订单');
  await page.getByRole('button', { name: '发送消息' }).click();

  await expect(page.getByText('读取完成')).toBeVisible();
  expect(calls.streams).toBe(1);
});

test('destructive tool shows a read-only card linking to the approval center', async ({ page }) => {
  await installHarness(page, { approvals: [pendingApproval()] });
  await openChat(page);

  await expect(page.getByText('工具 delete_order 等待审批', { exact: true })).toBeVisible();
  // M3/M4:对话页不再提供批准/拒绝按钮,审批操作收敛到审批中心。
  await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '拒绝' })).toHaveCount(0);
  const goCenter = page.getByRole('button', { name: '前往审批中心' });
  await expect(goCenter).toBeVisible();
  await goCenter.click();
  await page.waitForURL('**/approvals');
});

test('approved approval offers a manual resume command on the read-only card', async ({ page }) => {
  await installHarness(page, { approvals: [pendingApproval('approved')] });
  await openChat(page);

  await expect(page.getByText('工具 delete_order 已批准', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '继续执行' })).toBeVisible();
  await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '拒绝' })).toHaveCount(0);
});

for (const [status, text] of [
  ['expired', '工具审批已过期'],
  ['authorization_denied', '权限已变更，工具执行已阻止'],
  ['unknown_outcome', '工具执行结果未知，需要人工对账'],
] as const) {
  test(`${status} is a terminal state without actions on the read-only card`, async ({ page }) => {
    await installHarness(page, { approvals: [pendingApproval(status)] });
    await openChat(page);

    await expect(page.getByText(text)).toBeVisible();
    // terminal 态:不提供"前往审批中心"导航与"继续执行",审批已定格。
    await expect(page.getByRole('button', { name: '前往审批中心' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '继续执行' })).toHaveCount(0);
  });
}

test('member sees the same read-only card without any command', async ({ page }) => {
  await installHarness(page, { role: 'member', approvals: [pendingApproval()] });
  await openChat(page);

  await expect(page.getByText('工具 delete_order 等待审批', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '前往审批中心' })).toBeVisible();
  await expect(page.getByRole('button', { name: '批准并继续' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '拒绝' })).toHaveCount(0);
  // 只读视角不暴露任何敏感字段(member 历史/详情 DTO 剔除恢复键,对话页同理)。
  await expect(page.getByText('cross-tenant-secret')).toHaveCount(0);
});
