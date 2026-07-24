import { expect, test, type Page, type Route } from '@playwright/test';

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const systemAgent = {
  id: 'stratum-platform-assistant',
  name: '平台使用小助手',
  description: '官方使用指导与当前租户只读诊断',
  llmModel: '',
  allowedSkills: [],
  mcpToolIds: [],
  knowledgeWorkspaceIds: [],
  isSystem: true,
  managementMode: 'platform',
};

const ordinaryAgent = {
  id: 'ordinary-agent',
  name: '普通 Agent',
  description: '租户 Agent',
  llmModel: 'tenant-model',
  allowedSkills: [],
  mcpToolIds: [],
  knowledgeWorkspaceIds: [],
};

const artifacts = [
  {
    type: 'citations',
    profileVersion: '2026-07-23.v1',
    citations: [{
      documentId: 'agent-guide', title: 'Agent 使用指南', productVersion: '2026.07',
      section: '创建对话', url: 'https://docs.example.invalid/agent/chat', excerpt: '选择系统内置助手后创建会话。',
    }],
  },
  {
    type: 'diagnostic_report',
    profileVersion: '2026-07-23.v1',
    diagnosticReport: {
      facts: [{
        area: 'agent', statement: '当前 Agent 执行记录可读取', source: 'agent_execution',
        observedAt: '2026-07-23T00:00:00Z',
      }],
      inferences: [],
      evidenceGaps: [{ area: 'mcp', source: 'mcp_status', code: 'evidence_unavailable' }],
      recommendedActions: ['检查 MCP 配置状态'],
      citations: [],
      steps: [
        { tool: 'stratum_search_official_docs', outcome: 'success', latencyMs: 4 },
        { tool: 'stratum_diagnose_tenant', outcome: 'gap', errorCode: 'evidence_unavailable', latencyMs: 7 },
      ],
    },
  },
];

async function installHarness(page: Page, role: 'member' | 'admin', ready = false) {
  await page.route('**/auth/refresh', (route) => json(route, { access_token: 'browser-fixture-token' }));
  await page.route('**/auth/me', (route) => json(route, { sub: `${role}-user`, tenant_id: 'tenant-a', role }));
  await page.route('**/api/v1/tenant/settings', (route) =>
    json(route, { tenant_id: 'tenant-a', tenant_name: '助手验收租户', settings: {} }));
  await page.route('**/tenant/list', (route) =>
    json(route, { tenants: [{ tenant_id: 'tenant-a', name: '助手验收租户' }] }));
  await page.route('**/agents/tool-approvals', (route) => json(route, { approvals: [] }));
  await page.route('**/agents/system/settings', (route) =>
    json(route, { agentId: systemAgent.id, llmModel: ready ? 'deterministic-e2e' : '', ready }));
  await page.route('**/models', (route) => json(route, { models: [] }));
  await page.route('**/agents', (route) => json(route, { agents: [ordinaryAgent, { ...systemAgent, llmModel: ready ? 'deterministic-e2e' : '' }] }));
  await page.route(`**/agents/${systemAgent.id}/conversations`, (route) =>
    json(route, { conversations: [{ id: 'assistant-conversation', agent_id: systemAgent.id, name: '平台验收会话' }] }));
  await page.route('**/conversations/assistant-conversation/messages*', (route) => json(route, {
    messages: [{
      id: 'assistant-message', role: 'assistant', content: '已完成官方检索和当前租户诊断。',
      created_at: new Date().toISOString(), artifacts,
    }],
  }));
}

async function openAssistant(page: Page) {
  await page.goto('/chat');
  if (page.viewportSize()!.width < 768) {
    await page.getByRole('button', { name: '打开会话列表' }).click();
    const drawer = page.getByRole('dialog', { name: '会话列表' });
    await expect(drawer).toBeVisible();
    await expect(drawer.getByText('平台使用小助手')).toBeVisible();
    await drawer.getByText('平台验收会话').click();
  } else {
    await page.getByText('平台验收会话').click();
  }
  await expect(page.getByText('平台使用小助手').last()).toBeVisible();
  await expect(page.getByText('系统内置').last()).toBeVisible();
}

async function expectNoOverlap(page: Page) {
  const overlaps = await page.locator('.agent-chat-page').evaluate((root) => {
    const selectors = ['.chat-message-bubble', '.diagnostic-report', '.ant-collapse-header'];
    const boxes = selectors.flatMap((selector) =>
      Array.from(root.querySelectorAll<HTMLElement>(selector)).map((node) => ({
        selector,
        rect: node.getBoundingClientRect(),
        scrollWidth: node.scrollWidth,
        clientWidth: node.clientWidth,
      })),
    );
    return boxes.filter(({ rect, scrollWidth, clientWidth }) =>
      rect.left < 0 || rect.right > window.innerWidth + 1 || scrollWidth > clientWidth + 1,
    ).map(({ selector }) => selector);
  });
  expect(overlaps).toEqual([]);
}

test.beforeEach(async ({ page }, testInfo) => {
  test.skip(!['mobile-390', 'desktop-1440'].includes(testInfo.project.name), 'acceptance viewport matrix');
  await installHarness(page, 'member');
});

test('member sees system-first managed assistant, no-model guidance, and structured evidence', async ({ page }, testInfo) => {
  await openAssistant(page);
  await expect(page.getByText('尚未配置模型，请联系租户管理员')).toBeVisible();
  await expect(page.getByRole('button', { name: '设置助手模型' })).toHaveCount(0);
  await expect(page.getByText('已完成官方检索和当前租户诊断。')).toBeVisible();

  const reports = page.getByText('诊断证据');
  for (let index = 0; index < await reports.count(); index += 1) {
    await reports.nth(index).click();
  }
  await expect(page.getByText('当前 Agent 执行记录可读取')).toBeVisible();
  await expect(page.getByText('evidence_unavailable').first()).toBeVisible();
  await expect(page.getByText('Agent 使用指南 · 创建对话')).toBeVisible();
  await expect(page.getByText('stratum_diagnose_tenant')).toBeVisible();
  await expectNoOverlap(page);
  await page.screenshot({ path: testInfo.outputPath('system-assistant.png'), fullPage: true });
});

test('admin gets only the managed model settings entry and no resource mutation entry', async ({ page }, testInfo) => {
  await page.unrouteAll({ behavior: 'wait' });
  await installHarness(page, 'admin');
  await openAssistant(page);
  await expect(page.getByRole('button', { name: '设置助手模型' })).toBeVisible();
  await expect(page.getByText('编辑 Agent')).toHaveCount(0);
  await expect(page.getByText('删除 Agent')).toHaveCount(0);
  await expectNoOverlap(page);
  await page.screenshot({ path: testInfo.outputPath('system-assistant-admin.png'), fullPage: true });
});
