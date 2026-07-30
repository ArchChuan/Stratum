import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { configureManagedModels, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface CrossPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }
const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const chooseOption = async (page: Page, label: string, searchValue: string, optionName: string) => {
  const select = page.locator('.ant-form-item').filter({ hasText: label }).locator('.ant-select');
  await select.locator('.ant-select-selector').click();
  await select.locator('input').fill(searchValue);
  await page.locator('.ant-select-item-option-content').filter({ hasText: optionName }).click();
};
const findServerRow = async (page: Page, serverName: string) => {
  const row = page.locator('tr').filter({ hasText: serverName });
  const activePage = page.locator('.ant-pagination-item-active');
  if (await activePage.count() > 0 && (await activePage.textContent())?.trim() !== '1') {
    await page.locator('.ant-pagination-item[title="1"]').click();
    await expect(activePage).toHaveText('1');
  }
  while (await row.count() === 0) {
    const next = page.getByRole('button', { name: 'right' });
    if (await next.isDisabled()) break;
    const currentPage = (await activePage.textContent())?.trim() ?? '';
    await next.click();
    await expect(activePage).not.toHaveText(currentPage);
  }
  await expect(row).toBeVisible();
  return row;
};

export const executeAgentSkillMCPPack = async ({
  actor, pool, evidence, webURL,
}: CrossPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  await configureManagedModels(pool, tenantID);
  const page = await actor.context.newPage();
  const suffix = Date.now();
  const serverName = `E2E-Cross-MCP-${suffix}`;
  const skillName = `E2E-Cross-Skill-${suffix}`;
  const agentName = `E2E-Cross-Agent-${suffix}`;
  let serverID = '';
  let skillID = '';
  let agentID = '';
  try {
    await page.goto(`${webURL}/mcp/create`);
    await page.getByLabel('名称').fill(serverName);
    const transportSelect = page.locator('.ant-form-item').filter({ hasText: '传输协议' }).locator('.ant-select');
    await transportSelect.locator('.ant-select-selector').click();
    await page.getByLabel('传输协议').press('ArrowDown');
    await page.getByLabel('传输协议').press('Enter');
    await page.getByLabel('服务器 URL').fill('http://127.0.0.1:19091/mcp');
    const mcpResponse = waitForMutation(page, '/mcp/servers', 'POST');
    await page.getByRole('button', { name: '添加服务器' }).click();
    const mcpCreated = await mcpResponse;
    expect(mcpCreated.status()).toBe(201);
    serverID = (await mcpCreated.json() as { server_id: string }).server_id;

    await page.goto(`${webURL}/skills/create`);
    await page.getByLabel('名称').fill(skillName);
    await page.getByLabel('能力目标').fill('通过 MCP 回显 stateful 输入');
    await page.getByLabel('调用时机').fill('用户要求执行 stateful MCP 时');
    await page.getByLabel('样例输入').fill('执行 stateful MCP');
    await page.getByLabel('期望输出').fill('stateful MCP call completed');
    await page.getByLabel('执行指令').fill('必须调用绑定的 MCP 工具并返回结果。');
    await page.getByLabel('所需 MCP 工具').fill(`mcp:${serverID}:stateful_echo`);
    const skillResponse = waitForMutation(page, '/skills', 'POST');
    await page.getByRole('button', { name: '创建草稿' }).click();
    const skillCreated = await skillResponse;
    expect(skillCreated.status()).toBe(201);
    skillID = (await skillCreated.json() as { skill: { id: string } }).skill.id;
    await page.getByRole('tab', { name: '激活契约' }).click();
    await page.getByLabel('激活名称').fill('stateful_mcp_echo');
    await page.getByLabel('用途说明').fill('调用 stateful MCP 回显工具');
    await page.getByLabel('确认契约').click();
    const activationResponse = waitForMutation(page, `/skills/${skillID}/draft/activation`, 'PATCH');
    await page.getByRole('button', { name: '保存激活契约' }).click();
    expect((await activationResponse).status()).toBe(200);
    await page.getByRole('tab', { name: 'Revision' }).click();
    const publishResponse = waitForMutation(page, `/skills/${skillID}/publish`, 'POST');
    await page.getByRole('button', { name: '发布当前 Revision' }).click();
    expect((await publishResponse).status()).toBe(200);

    const skillsListResponse = page.waitForResponse((response) => (
      new URL(response.url()).pathname === '/skills' && response.request().method() === 'GET'
    ));
    const toolOptionsResponse = page.waitForResponse((response) => (
      new URL(response.url()).pathname === `/mcp/servers/${serverID}/tools`
      && response.request().method() === 'GET'
    ));
    await page.goto(`${webURL}/agents/create`);
    const [skillsListed, toolsListed] = await Promise.all([skillsListResponse, toolOptionsResponse]);
    expect(skillsListed.status()).toBe(200);
    const skillsBody = JSON.stringify(await skillsListed.json());
    expect(skillsBody).toContain(skillID);
    expect(skillsBody).toContain(skillName);
    expect(toolsListed.status()).toBe(200);
    expect(JSON.stringify(await toolsListed.json())).toContain('stateful_echo');
    await page.getByLabel('名称').fill(agentName);
    await page.getByLabel('系统提示词').fill('必须激活可用 Skill 并调用 MCP 工具。');
    await page.getByLabel('LLM 模型').click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option')
      .filter({ hasText: /^qwen-max$/ }).click();
    await chooseOption(page, '技能', skillID, skillName);
    await chooseOption(page, 'MCP 工具', `mcp:${serverID}:stateful_echo`, `${serverName} / stateful_echo`);
    const agentResponse = waitForMutation(page, '/agents', 'POST');
    await page.getByRole('button', { name: '创建 Agent' }).click();
    const agentCreated = await agentResponse;
    expect(agentCreated.status()).toBe(201);
    agentID = (await agentCreated.json() as { id: string }).id;
    expect(await rows<{ skill_id: string }>(pool, tenantID,
      'SELECT skill_id FROM agent_skill_links WHERE agent_id=$1', [agentID])).toEqual([{ skill_id: skillID }]);
    expect(await rows<{ server_id: string; tool_name: string }>(pool, tenantID,
      'SELECT server_id,tool_name FROM agent_mcp_tool_links WHERE agent_id=$1', [agentID]))
      .toEqual([{ server_id: serverID, tool_name: 'stateful_echo' }]);

    await page.evaluate((id) => sessionStorage.setItem('chat:lastAgentId', id), agentID);
    await page.goto(`${webURL}/chat`);
    await expect(page.getByText(agentName, { exact: true }).first()).toBeVisible();
    await page.getByRole('button', { name: '新建会话' }).click();
    await page.getByPlaceholder(/输入消息/).fill('请调用 MCP 工具完成 stateful 验收');
    await page.getByRole('button', { name: '发送消息' }).click();
    await expect(page.getByText('工具 stateful_echo 等待审批', { exact: true })).toBeVisible({ timeout: 120_000 });

    const decisionResponse = page.waitForResponse((response) => (
      /\/agents\/tool-approvals\/[^/]+\/decision$/.test(new URL(response.url()).pathname)
      && response.request().method() === 'POST'
    ));
    const resumeResponse = page.waitForResponse((response) => (
      /\/agents\/tool-approvals\/[^/]+\/resume$/.test(new URL(response.url()).pathname)
      && response.request().method() === 'POST'
    ));
    await page.getByRole('button', { name: '批准并继续' }).click();
    expect((await decisionResponse).status()).toBe(200);
    expect((await resumeResponse).status()).toBe(200);
    await expect(page.getByText('stateful sync completed', { exact: true }).last()).toBeVisible({ timeout: 120_000 });
    const approvals = await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM agent_tool_approvals WHERE agent_id=$1 ORDER BY created_at DESC LIMIT 1', [agentID]);
    expect(approvals).toEqual([{ status: 'executed' }]);
    evidence.ui.push('Agent Skill MCP approval and resume completed through Chromium controls');
    evidence.http.push('Agent Skill MCP decision, resume, provider, and tool calls succeeded');
    evidence.database.push('Agent Skill MCP approval reached executed state');

    await page.goto(`${webURL}/agents`);
    const agentCard = page.locator('.ant-card').filter({ hasText: agentName });
    await agentCard.getByRole('button', { name: '删除 Agent' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    await page.goto(`${webURL}/skills`);
    const skillCard = page.locator('.ant-card').filter({ hasText: skillName });
    await skillCard.getByRole('button', { name: '删除技能' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    const mcpListResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await page.goto(`${webURL}/mcp`);
    const mcpList = await mcpListResponse;
    expect(mcpList.status()).toBe(200);
    expect(JSON.stringify(await mcpList.json())).toContain(serverName);
    const mcpRow = await findServerRow(page, serverName);
    const deleteMCPResponse = waitForMutation(page, `/mcp/servers/${serverID}/config`, 'DELETE');
    await mcpRow.getByRole('button', { name: '删除' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteMCPResponse).status()).toBe(200);
    expect(await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM mcp_configs WHERE id=$1', [serverID])).toEqual([{ count: '0' }]);
  } finally {
    await page.close();
  }
  return [
    'agent.mutation.post.agents.tool.approvals.id.decision',
    'agent.mutation.post.agents.tool.approvals.id.resume',
  ];
};
