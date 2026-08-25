import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import { restoreActorSession, type BrowserActor } from '../core/actors';
import { addGeneratedActorMembership, configureManagedModels, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { openAgentCreation } from '../core/navigation';

interface CrossPackContext { actor: BrowserActor; approver: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string; fixtureURL: string; backendURL: string }
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
  actor, approver, pool, evidence, webURL, fixtureURL, backendURL,
}: CrossPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  await configureManagedModels(pool, tenantID, fixtureURL, actor.accessToken ?? '', backendURL);
  const page = await actor.context.newPage();
  let approverPage: Page | null = null;
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
    await page.getByLabel('服务器 URL').fill(`${fixtureURL}/mcp`);
    const mcpResponse = waitForMutation(page, '/mcp/servers', 'POST');
    await page.getByRole('button', { name: '添加服务器' }).click();
    const mcpCreated = await mcpResponse;
    expect(mcpCreated.status()).toBe(201);
    serverID = (await mcpCreated.json() as { server_id: string }).server_id;

    await page.goto(`${webURL}/skills/create`);
    await page.getByLabel('名称').fill(skillName);
    await page.getByLabel('描述').fill('通过 MCP 回显 stateful 输入');
    await page.getByLabel('执行指令').fill('必须调用绑定的 MCP 工具并返回结果。');
    // skill 不再直接绑定 MCP 工具（d65933db 移除 skill requirements）；工具绑定
    // 在下方 agent 创建步骤通过「MCP 工具」Select 完成，并由 agent_mcp_tool_links 断言。
    // 三字段模型（3d037c86 简化）：保存即生效，无激活契约/发布步骤。
    const skillResponse = waitForMutation(page, '/skills', 'POST');
    await page.getByRole('button', { name: /创\s*建/ }).click();
    const skillCreated = await skillResponse;
    expect(skillCreated.status()).toBe(201);
    skillID = (await skillCreated.json() as { skill: { id: string } }).skill.id;

    const skillsListResponse = page.waitForResponse((response) => (
      new URL(response.url()).pathname === '/skills' && response.request().method() === 'GET'
    ));
    const toolOptionsResponse = page.waitForResponse((response) => (
      new URL(response.url()).pathname === `/mcp/servers/${serverID}/tools`
      && response.request().method() === 'GET'
    ));
    await openAgentCreation(page);
    const [skillsListed, toolsListed] = await Promise.all([skillsListResponse, toolOptionsResponse]);
    expect(skillsListed.status()).toBe(200);
    const skillsBody = JSON.stringify(await skillsListed.json());
    expect(skillsBody).toContain(skillID);
    expect(skillsBody).toContain(skillName);
    expect(toolsListed.status()).toBe(200);
    expect(JSON.stringify(await toolsListed.json())).toContain('stateful_echo');
    await page.getByLabel('名称').fill(agentName);
    await page.getByLabel('系统提示词').fill('必须激活可用 Skill 并调用 MCP 工具。');
    const modelInput = page.getByRole('combobox', { name: 'LLM 模型' });
    await modelInput.fill('qwen-max');
    await modelInput.press('Enter');
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

    // 自审批保护（Decide 校验 actor != payload.UserID）：审批必须由发起者之外的
    // admin/owner 决定。approver（systemAdmin）以 owner 身份加入本租户后经真实
    // switch-tenant 切换会话批准。传展开对象避免污染 actors.systemAdmin 的 tenantID。
    const approverID = requireUUID(approver.userID ?? '', 'user_id');
    await addGeneratedActorMembership(pool, tenantID, approverID, 'owner');
    await restoreActorSession({ ...approver, tenantID }, backendURL);
    approverPage = await approver.context.newPage();
    // M3/M4:审批操作收敛到审批中心(/approvals),对话页不再提供批准/拒绝按钮。
    // 审批人从工作台批准;发起人 page 保持打开,轮询 active-execution 检测到
    // approved 后自动流式续跑(execute/stream),输出回流发起人原会话。
    await approverPage.goto(`${webURL}/approvals`);
    await expect(approverPage.getByText('工具审批', { exact: true })).toBeVisible({ timeout: 120_000 });
    const approvalRow = approverPage.locator('tr').filter({ hasText: 'stateful_echo' }).first();
    await expect(approvalRow).toBeVisible({ timeout: 120_000 });
    const decisionResponse = approverPage.waitForResponse((response) => (
      /\/agents\/tool-approvals\/[^/]+\/decision$/.test(new URL(response.url()).pathname)
      && response.request().method() === 'POST'
    ));
    await approvalRow.getByRole('button', { name: '批准' }).click();
    // Modal okText 双汉字会被 antd 自动插空格("批准"→"批 准")，用正则同时覆盖两种文案。
    await approverPage.locator('.ant-modal').getByRole('button', { name: /批\s*准/ }).click();
    expect((await decisionResponse).status()).toBe(200);
    // 发起人 page 轮询 active-execution 自动续跑,SSE 输出流式回流(复用原消息追加)。
    // 续跑走 execute/stream 流式,fake LLM 对 stream 返回 "stateful stream completed"
    // (sync 文案 "stateful sync completed" 仅非流式 execute 返回)。
    await expect(page.getByText('stateful stream completed', { exact: true }).last()).toBeVisible({ timeout: 120_000 });
    const approvals = await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM agent_tool_approvals WHERE agent_id=$1 ORDER BY created_at DESC LIMIT 1', [agentID]);
    expect(approvals).toEqual([{ status: 'executed' }]);
    evidence.ui.push('Agent Skill MCP approval from approval center with automatic streaming resume');
    evidence.http.push('Agent Skill MCP decision and resumed execute stream succeeded');
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
    if (approverPage) await approverPage.close();
  }
  return [
    'agent.mutation.post.agents.tool.approvals.id.decision',
    'agent.mutation.post.agents.id.execute.stream',
  ];
};
