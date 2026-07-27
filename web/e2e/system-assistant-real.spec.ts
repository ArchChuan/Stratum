import { expect, test, type Page } from '@playwright/test';

import {
  createPlatformAssistantSession,
  getProposalAsSession,
  proposalApplyEventEvidence,
  proposalDatabaseEvidence,
  requireUUID,
  seedTerminalProposal,
  workspaceDatabaseEvidence,
} from './support/real-platform-assistant';

test.skip(process.env.REAL_PLATFORM_ASSISTANT_E2E !== '1', 'set REAL_PLATFORM_ASSISTANT_E2E=1');

const openConversationControls = async (page: Page) => {
  if (page.viewportSize()!.width < 768) {
    await page.getByRole('button', { name: '打开会话列表' }).click();
    return page.getByRole('dialog', { name: '会话列表' });
  }
  return page;
};

const sensitiveMarkers = [
  'platform-assistant-browser-e2e-key',
  'Authorization:',
  'Bearer ',
  'apiKey',
  'password',
];

test('real admin chat creates, edits, reloads, and applies one governed proposal', async (
  { browser, context, page },
  testInfo,
) => {
  const session = await createPlatformAssistantSession(context, 'admin');
  await page.goto('/chat');
  await expect(page.getByText('Stratum 平台助手').first()).toBeVisible();

  const controls = await openConversationControls(page);
  const createResponse = page.waitForResponse((response) =>
    response.url().includes('/agents/stratum-platform-assistant/conversations') &&
    response.request().method() === 'POST',
  );
  await controls.getByRole('button', { name: '新建会话' }).click();
  expect((await createResponse).status()).toBe(201);
  if (page.viewportSize()!.width < 768) await page.keyboard.press('Escape');

  await page.getByPlaceholder(/输入消息/).fill('请创建一个经过治理的知识库变更提案。');
  const executeResponse = page.waitForResponse((response) =>
    response.url().includes('/agents/stratum-platform-assistant/execute/stream') &&
    response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: '发送消息' }).click();
  expect((await executeResponse).status()).toBe(200);
  const reviewLink = page.getByRole('link', { name: '审阅变更' });
  await expect(reviewLink).toBeVisible();
  await reviewLink.click();
  await expect(page.getByRole('heading', { name: '审阅资源变更' })).toBeVisible();
  const proposalId = requireUUID(page.url().split('/').pop() || '', 'proposal_id');

  const revisedDescription = `E2E-已核验知识-${Date.now()}`;
  await page.getByRole('textbox', { name: '说明' }).fill(revisedDescription);
  const patchResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/resource-change-proposals/${proposalId}`) &&
    response.request().method() === 'PATCH',
  );
  await page.getByRole('button', { name: '保存调整' }).click();
  expect((await patchResponse).status()).toBe(200);
  await expect(page.getByText('提案已保存')).toBeVisible();

  await page.reload();
  await expect(page.getByText('已调整 1 次')).toBeVisible();
  await expect(page.getByRole('textbox', { name: '说明' })).toHaveValue(revisedDescription);

  const confirmResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/resource-change-proposals/${proposalId}/confirm`) &&
    response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: '确认并应用' }).click();
  const dialog = page.locator('.ant-modal:visible');
  await dialog.getByRole('button', { name: '确认并应用' }).click();
  const confirmed = await confirmResponse;
  expect(confirmed.status()).toBe(200);
  const confirmBody = await confirmed.json() as { applyResult: { resourceId: string } };
  const workspaceId = requireUUID(confirmBody.applyResult.resourceId, 'workspace_id');
  await expect(page.getByText('已应用').first()).toBeVisible();
  await expect(page.getByRole('button', { name: '确认并应用' })).toHaveCount(0);

  expect(proposalDatabaseEvidence(session.tenantId, proposalId)).toBe(`applied|1|${workspaceId}`);
  expect(proposalApplyEventEvidence(session.tenantId, proposalId)).toBe('1|1');
  expect(workspaceDatabaseEvidence(session.tenantId, workspaceId)).toContain(`|${revisedDescription}`);

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
  expect(overflow).toBe(false);
  await expect(page.locator('canvas')).toHaveCount(0);
  const visibleText = await page.locator('body').innerText();
  for (const marker of sensitiveMarkers) expect(visibleText).not.toContain(marker);
  const screenshot = await page.screenshot({
    path: testInfo.outputPath('platform-assistant-applied.png'),
    fullPage: true,
  });
  expect(screenshot.byteLength).toBeGreaterThan(5_000);
  const screenshotBytes = screenshot.toString('latin1');
  for (const marker of sensitiveMarkers) expect(screenshotBytes).not.toContain(marker);

  for (const terminal of [
    { status: 'stale', label: '基线冲突', reason: '目标资源已变化' },
    { status: 'expired', label: '已过期', reason: '审阅期限已结束' },
    { status: 'failed', label: '应用失败', reason: '系统不会自动重试' },
    { status: 'unknown_outcome', label: '结果未知', reason: '为避免重复写入' },
  ] as const) {
    const terminalId = seedTerminalProposal(session, terminal.status);
    await page.goto(`/resource-change-proposals/${terminalId}`);
    await expect(page.getByText(terminal.label).first()).toBeVisible();
    await expect(page.getByText(new RegExp(terminal.reason))).toBeVisible();
    await expect(page.getByRole('button', { name: '确认并应用' })).toHaveCount(0);
  }

  const memberContext = await browser.newContext();
  try {
    const memberSession = await createPlatformAssistantSession(memberContext, 'member');
    const memberPage = await memberContext.newPage();
    await memberPage.goto(`/resource-change-proposals/${proposalId}`);
    await expect(memberPage.getByText('仅管理员可访问此页面，普通成员无权限。')).toBeVisible();
    const denied = await getProposalAsSession(memberContext, memberSession, proposalId);
    expect(denied.status()).toBe(403);
    await expect(memberPage.getByRole('button', { name: '确认并应用' })).toHaveCount(0);
  } finally {
    await memberContext.close();
  }
});
