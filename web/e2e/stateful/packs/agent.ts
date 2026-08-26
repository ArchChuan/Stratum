import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { configureManagedModels, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface AgentPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string; fixtureURL: string; backendURL: string }
const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => {
  const resourceType = response.request().resourceType();
  return (resourceType === 'xhr' || resourceType === 'fetch') &&
    new URL(response.url()).pathname === path && response.request().method() === method;
});
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};

export const executeAgentPack = async ({ actor, pool, evidence, webURL, fixtureURL, backendURL }: AgentPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const agentName = `E2E-Agent-${Date.now()}`;
  let agentID = '';
  let conversationID = '';
  try {
    await configureManagedModels(pool, tenantID, fixtureURL, actor.accessToken ?? '', backendURL);
    const listResponse = waitForMutation(page, '/agents', 'GET');
    await page.goto(`${webURL}/agents`);
    expect((await listResponse).status()).toBe(200);
    await expect(page).toHaveURL(`${webURL}/agents`);
    await expect(page.getByRole('heading', { name: 'Agent 列表' })).toBeVisible();
    await expect(page.getByText('平台使用助手', { exact: true }).first()).toBeVisible();
    completed.push('agent.route.agents', 'agent.route.agents.includesPlatformSeed');

    const catResp = waitForMutation(page, '/admin/models', 'GET');
    await page.getByRole('button', { name: '创建 Agent' }).click();
    await expect(page).toHaveURL(`${webURL}/agents/create`);
    const catalogue = await catResp;
    expect(catalogue.status()).toBe(200);
    const catalogueBody = await catalogue.json() as { models?: Array<{ name: string }> };
    const chatModels = catalogueBody.models?.map(({ name }) => name);
    expect(chatModels, `chat models=${chatModels?.join(',')}`).toContain('qwen-max');
    completed.push('agent.route.agents.create');

    await page.getByLabel('名称').fill(agentName);
    await page.getByLabel('描述').fill('全系统 stateful Agent 验收');
    await page.getByLabel('系统提示词').fill('请简洁回答，并明确包含 stateful。');
    const createModelInput = page.getByRole('combobox', { name: 'LLM 模型' });
    await createModelInput.scrollIntoViewIfNeeded();
    await createModelInput.click({ force: true });
    await createModelInput.fill('qwen-max');
    await createModelInput.press('Enter');
    await expect(page.getByRole('slider', { name: '最大迭代次数' })).toHaveAttribute('aria-valuemax', '90');

    const createResponse = waitForMutation(page, '/agents', 'POST');
    const createdListResponse = waitForMutation(page, '/agents', 'GET');
    await page.getByRole('button', { name: '创建 Agent' }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    agentID = (await created.json() as { id: string }).id;
    await expect(page).toHaveURL(`${webURL}/agents`);
    expect((await createdListResponse).status()).toBe(200);
    completed.push('agent.mutation.post.agents');
    recordEvidence(evidence, 'Agent creation');

    const card = page.locator('.ant-card').filter({ hasText: agentName });
    await card.getByRole('button', { name: '编辑 Agent' }).click();
    await expect(page).toHaveURL(`${webURL}/agents/${agentID}/edit`);
    completed.push('agent.route.agents.id.edit');

    await page.getByLabel('描述').fill('全系统 stateful Agent 验收，已更新');
    const updateResponse = waitForMutation(page, `/agents/${agentID}`, 'PUT');
    // versioning: 保存后编辑页原地刷新（reloadAgent 重拉详情），不再跳转列表页。
    const detailReloadResponse = waitForMutation(page, `/agents/${agentID}`, 'GET');
    await page.getByRole('button', { name: '保存修改' }).click();
    expect((await updateResponse).status()).toBe(200);
    expect((await detailReloadResponse).status()).toBe(200);
    expect(await rows<{ description: string; max_iterations: number }>(pool, tenantID,
      'SELECT description,max_iterations FROM agents WHERE id=$1', [agentID]))
      .toEqual([{ description: '全系统 stateful Agent 验收，已更新', max_iterations: 10 }]);
    completed.push('agent.mutation.put.agents.id');

    // versioning: 保存后停在编辑页，显式返回列表页继续后续执行/删除流程。
    await page.goto(`${webURL}/agents`);
    await expect(page).toHaveURL(`${webURL}/agents`);

    const updatedCard = page.locator('.ant-card').filter({ hasText: agentName });
    await updatedCard.getByRole('button', { name: '执行 Agent' }).click();
    await page.getByPlaceholder('输入你希望 Agent 执行的任务...').fill('只回复：stateful sync completed');
    const executeResponse = waitForMutation(page, `/agents/${agentID}/execute`, 'POST');
    await page.locator('.ant-modal').getByRole('button', { name: /执\s*行/ }).click();
    const executed = await executeResponse;
    const executionBody = await executed.json() as { output?: string; steps?: unknown[]; error?: string };
    expect(executed.status(), executionBody.error || 'Agent execution failed').toBe(200);
    expect(executionBody.output).toBeTruthy();
    completed.push('agent.mutation.post.agents.id.execute');
    recordEvidence(evidence, 'Agent synchronous execution');
    await page.locator('.ant-modal').getByRole('button', { name: /关\s*闭/ }).click();

    await page.evaluate((id) => sessionStorage.setItem('chat:lastAgentId', id), agentID);
    const chatAgentsResponse = waitForMutation(page, '/agents', 'GET');
    await page.goto(`${webURL}/chat`);
    const chatAgents = await chatAgentsResponse;
    expect(chatAgents.status()).toBe(200);
    const chatAgentBody = await chatAgents.json() as { agents?: Array<{ id: string; name: string }> };
    expect(chatAgentBody.agents?.some(({ id }) => id === agentID),
      `chat agent list did not include generated Agent; names=${chatAgentBody.agents?.map(({ name }) => name).join(',')}`).toBe(true);
    await expect(page.getByRole('heading', { name: 'Agent 对话' })).toBeVisible();
    completed.push('agent.route.chat');
    await expect(page.getByText(agentName, { exact: true }).first()).toBeVisible();

    const conversationResponse = waitForMutation(page, `/agents/${agentID}/conversations`, 'POST');
    await page.getByRole('button', { name: '新建会话' }).click();
    const conversation = await conversationResponse;
    expect(conversation.status()).toBe(201);
    conversationID = (await conversation.json() as { id: string }).id;
    completed.push('agent.mutation.post.agents.agentid.conversations');

    await page.getByRole('button', { name: '重命名' }).click();
    const renameInput = page.locator('.agent-chat-page input.ant-input').last();
    await renameInput.fill('E2E Stateful Conversation');
    const renameResponse = waitForMutation(page, `/conversations/${conversationID}`, 'PATCH');
    await renameInput.press('Enter');
    expect((await renameResponse).status()).toBe(204);
    completed.push('agent.mutation.patch.conversations.convid');

    const streamResponse = waitForMutation(page, `/agents/${agentID}/execute/stream`, 'POST');
    await page.getByPlaceholder(/输入消息/).fill('只回复：stateful stream completed');
    await page.getByRole('button', { name: '发送消息' }).click();
    expect((await streamResponse).status()).toBe(200);
    await expect(page.getByText(/stateful/i).last()).toBeVisible({ timeout: 120_000 });
    completed.push('agent.mutation.post.agents.id.execute.stream', 'agent.mutation.post.conversations.convid.messages');
    await expect.poll(async () => Number((await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM chat_messages WHERE conversation_id=$1', [conversationID]))[0].count),
    { timeout: 120_000 }).toBeGreaterThanOrEqual(2);
    recordEvidence(evidence, 'Agent streaming conversation');

    const deleteConversationResponse = waitForMutation(page, `/conversations/${conversationID}`, 'DELETE');
    await page.getByRole('button', { name: '删除' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteConversationResponse).status()).toBe(204);
    completed.push('agent.mutation.delete.conversations.convid');

    await page.goto(`${webURL}/agents`);
    const systemCard = page.locator('.ant-card').filter({ hasText: '平台使用助手' });
    await systemCard.getByRole('button', { name: '编辑 Agent' }).click();
    await expect(page).toHaveURL(`${webURL}/agents/stratum-platform-assistant/edit`);
    await expect(page.getByRole('heading', { name: '编辑 Agent' })).toBeVisible();

    const settingsModelInput = page.getByRole('combobox', { name: 'LLM 模型' });
    await expect(settingsModelInput).toBeEnabled();
    await settingsModelInput.scrollIntoViewIfNeeded();
    await settingsModelInput.click({ force: true });
    await settingsModelInput.press('ArrowDown');
    await settingsModelInput.press('Enter');
    const settingsResponse = waitForMutation(page, '/agents/stratum-platform-assistant', 'PUT');
    await page.getByRole('button', { name: '保存修改' }).click();
    expect((await settingsResponse).status()).toBe(200);
    const savedSystemModel = await rows<{ llm_model: string }>(pool, tenantID,
      "SELECT llm_model FROM agents WHERE system_key='stratum.platform_assistant'", []);
    expect(savedSystemModel[0]?.llm_model).toBeTruthy();
    completed.push('agent.mutation.put.agents.platformSeed');
    recordEvidence(evidence, 'system assistant model settings');

    await page.goto(`${webURL}/agents`);
    const deleteCard = page.locator('.ant-card').filter({ hasText: agentName });
    const deleteResponse = waitForMutation(page, `/agents/${agentID}`, 'DELETE');
    await deleteCard.getByRole('button', { name: '删除 Agent' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteResponse).status()).toBe(200);
    completed.push('agent.mutation.delete.agents.id');
    recordEvidence(evidence, 'Agent deletion');
  } finally {
    await page.close();
  }
  return completed;
};
