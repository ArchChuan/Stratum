import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import {
  configureManagedModels, requireUUID, withTenantMutation, withTenantQuery, type DatabasePool,
} from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface CollabPackContext {
  actor: BrowserActor;      // memberA：协作任务创建/启动/取消
  adminActor: BrowserActor; // tenantAdmin：参与 agent 生命周期
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  fixtureURL: string;
}

const waitForMutation = (page: Page, path: string | RegExp, method: string) => page.waitForResponse((response) => {
  const urlPath = new URL(response.url()).pathname;
  const pathMatch = typeof path === 'string' ? urlPath === path : path.test(urlPath);
  return pathMatch && response.request().method() === method;
}, { timeout: 60_000 });
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};
// Ant Design 用 "确 定" 作为 Modal 默认 OK 按钮文本（zhCN locale）
const clickModalOK = (page: Page) => page.locator('.ant-modal-content')
  .getByRole('button', { name: /确\s*定/ }).click();
// 参与者下拉用搜索过滤而非依赖虚拟滚动渲染：agent 残留累积超过可视高度后，
// rc-virtual-list 不渲染列表底部的 option，直接 toBeVisible 必然超时；输入
// UUID 过滤后结果唯一，必在可视区（等价真实用户搜索参与者）。
const selectParticipant = async (page: Page, agentID: string) => {
  // 先点 selector 打开面板：antd 关闭态下搜索框带 readonly 属性，fill 会
  // 报 "element is not editable"（20s 超时）——打开后 readonly 移除才可输入
  await page.locator('.ant-form-item').filter({ hasText: '参与者' })
    .locator('.ant-select-selector').click();
  // Form.Item 必填标记使 combobox 可访问名为 "* 参与者"，用 regex 匹配
  await page.getByLabel(/参与者/).fill(agentID);
  const option = page.locator('.ant-select-item-option').filter({ hasText: agentID });
  await expect(option).toBeVisible({ timeout: 15_000 });
  await option.click();
};

const createAgentViaUI = async (page: Page, webURL: string, name: string): Promise<string> => {
  await page.goto(`${webURL}/agents`);
  await expect(page.getByRole('heading', { name: 'Agent 列表' })).toBeVisible();
  const catResp = waitForMutation(page, '/admin/models', 'GET');
  await page.getByRole('button', { name: '创建 Agent' }).click();
  await expect(page).toHaveURL(`${webURL}/agents/create`);
  expect((await catResp).status()).toBe(200);
  await page.getByLabel('名称').fill(name);
  await page.getByLabel('描述').fill('全系统 stateful 验收');
  await page.getByLabel('系统提示词').fill('请简洁回答，并明确包含 stateful。');
  const modelInput = page.getByRole('combobox', { name: 'LLM 模型' });
  await modelInput.scrollIntoViewIfNeeded();
  await modelInput.click({ force: true });
  await modelInput.fill('qwen-max');
  await modelInput.press('Enter');
  const createResponse = waitForMutation(page, '/agents', 'POST');
  const createdListResponse = waitForMutation(page, '/agents', 'GET');
  await page.getByRole('button', { name: '创建 Agent' }).click();
  const created = await createResponse;
  expect(created.status()).toBe(201);
  const agentID = (await created.json() as { id: string }).id;
  await expect(page).toHaveURL(`${webURL}/agents`);
  expect((await createdListResponse).status()).toBe(200);
  return requireUUID(agentID, 'agent_id');
};

/**
 * collab 协作安全与执行引擎：member 创建协作任务（顺序策略 2 参与者）→ 启动
 * （running + 2 步骤，worker 自然执行至 completed）→ 创建第二个任务在 created
 * 态立即取消（无步骤，零竞争，验证 canceled 终态）→ 轮询第一个任务 completed
 * 后清理三表与参与 agent。
 */
export const executeCollabPack = async ({ actor, adminActor, pool, evidence, webURL, fixtureURL }: CollabPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const actorID = requireUUID(actor.userID ?? '', 'user_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const adminPage = await adminActor.context.newPage();
  const suffix = Date.now();
  const startDesc = `E2E-Collab-${suffix}`;
  const cancelDesc = `E2E-Collab-Cancel-${suffix}`;
  const agentName1 = `E2E-CollabAgent-${suffix}-1`;
  const agentName2 = `E2E-CollabAgent-${suffix}-2`;
  let startID = '';
  let cancelID = '';
  try {
    await configureManagedModels(pool, tenantID, fixtureURL);

    // ── 1. 管理员创建 2 个参与 agent（模型目录是 admin 路由） ────────────────
    const agent1 = await createAgentViaUI(adminPage, webURL, agentName1);
    const agent2 = await createAgentViaUI(adminPage, webURL, agentName2);

    // ── 2. member 打开协作页 ──────────────────────────────────────────────────
    const listResponse = waitForMutation(page, '/collaborations', 'GET');
    // 参与者选项依赖挂载时的 GET /agents：显式等它 200，慢响应（soak 负载）
    // 由 toBeVisible 的长预算兜底，失败则直接暴露后端问题而非神秘超时
    const agentsResponse = waitForMutation(page, '/agents', 'GET');
    await page.goto(`${webURL}/collaborations`);
    expect((await listResponse).status()).toBe(200);
    expect((await agentsResponse).status()).toBe(200);
    await expect(page.getByRole('button', { name: '创建协作任务' })).toBeVisible();
    completed.push('collab.route.collaborations');

    // ── 3. 创建 collab B（start 场景，顺序策略 + 2 参与者） ───────────────────
    await page.getByRole('button', { name: '创建协作任务' }).click();
    await page.getByLabel('任务描述').fill(startDesc);
    await page.locator('.ant-form-item').filter({ hasText: '协作策略' })
      .locator('.ant-select-selector').click();
    await page.locator('.ant-select-item-option').filter({ hasText: '顺序' }).click();
    await selectParticipant(page, agent1);
    await selectParticipant(page, agent2);
    // multiple Select 选完不自动关闭：面板保持打开会拦截对"任务描述"的 pointer
    // 点击（hit-target 检查失败），故用 focus 触发 select blur 收起面板——
    // 等价真实用户点击外部，但绕过 actionability 检查（soak 多轮下必现）
    await page.getByLabel('任务描述').focus();
    const createResponse = waitForMutation(page, '/collaborations', 'POST');
    await clickModalOK(page);
    const created = await createResponse;
    expect(created.status()).toBe(201);
    startID = requireUUID((await created.json() as { id: string }).id, 'collaboration_id');
    const createdRow = (await rows<{ status: string; created_by: string; participants: string[] }>(
      pool, tenantID,
      'SELECT status, created_by, participants FROM collaborations WHERE id=$1', [startID]))[0];
    expect(createdRow).toBeTruthy();
    expect(createdRow!.status).toBe('created');
    expect(createdRow!.created_by).toBe(actorID);
    expect(createdRow!.participants).toEqual([agent1, agent2]);
    completed.push('collab.mutation.post.collaborations');
    recordEvidence(evidence, 'collaboration creation');

    // ── 4. 启动 B → running + 2 个步骤（worker 可能已认领，只断言步骤总数） ──
    const startRow = page.locator('tr').filter({ hasText: startDesc });
    await expect(startRow).toBeVisible();
    const startResponse = waitForMutation(page, `/collaborations/${startID}/start`, 'POST');
    await startRow.getByRole('button', { name: /启\s*动/ }).click();
    expect((await startResponse).status()).toBe(200);
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM collaborations WHERE id=$1', [startID]))[0]?.status).toBe('running');
    expect((await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM task_steps WHERE plan_id=$1', [startID]))[0]?.count).toBe('2');
    completed.push('collab.mutation.post.collaborations.id.start');
    recordEvidence(evidence, 'collaboration start');

    // ── 5. 创建 collab A（created 态）→ 立即取消（无步骤，零竞争） ────────────
    await page.getByRole('button', { name: '创建协作任务' }).click();
    await page.getByLabel('任务描述').fill(cancelDesc);
    await page.locator('.ant-form-item').filter({ hasText: '协作策略' })
      .locator('.ant-select-selector').click();
    await page.locator('.ant-select-item-option').filter({ hasText: '顺序' }).click();
    await selectParticipant(page, agent1);
    await selectParticipant(page, agent2);
    // 同上：multiple Select 面板打开时 focus 外部输入框触发 blur 收起，避免覆盖确定按钮
    await page.getByLabel('任务描述').focus();
    const cancelCreateResponse = waitForMutation(page, '/collaborations', 'POST');
    await clickModalOK(page);
    const cancelCreated = await cancelCreateResponse;
    expect(cancelCreated.status()).toBe(201);
    cancelID = requireUUID((await cancelCreated.json() as { id: string }).id, 'collaboration_id');
    // created 态取消：UpdateStatus 状态前置 IN ('created','running') 允许落库，
    // 无步骤可 claim → worker 不参与 → 确定性
    const cancelRow = page.locator('tr').filter({ hasText: cancelDesc });
    await expect(cancelRow).toBeVisible();
    const cancelResponse = waitForMutation(page, `/collaborations/${cancelID}/cancel`, 'POST');
    await cancelRow.getByRole('button', { name: /取\s*消/ }).click();
    await page.locator('.ant-modal-confirm').getByRole('button', { name: /确\s*定/ }).click();
    expect((await cancelResponse).status()).toBe(200);
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM collaborations WHERE id=$1', [cancelID]))[0]?.status).toBe('canceled');
    completed.push('collab.mutation.post.collaborations.id.cancel');
    recordEvidence(evidence, 'collaboration cancel');

    // ── 6. 轮询 B 到 completed（worker 250ms 轮询 + fixture LLM stub） ────────
    await expect.poll(async () => (await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM collaborations WHERE id=$1', [startID]))[0]?.status,
    { timeout: 120_000 }).toBe('completed');
    recordEvidence(evidence, 'collaboration execution completion');

    // ── 7. 清理：collab 三表 + 参与 agent（UI 删除） ─────────────────────────
    await withTenantMutation(pool, tenantID, {
      text: 'DELETE FROM task_steps WHERE plan_id IN ($1,$2)',
      values: [startID, cancelID],
    });
    await withTenantMutation(pool, tenantID, {
      text: 'DELETE FROM shared_contexts WHERE plan_id IN ($1,$2)',
      values: [startID, cancelID],
    });
    await withTenantMutation(pool, tenantID, {
      text: 'DELETE FROM collaborations WHERE id IN ($1,$2)',
      values: [startID, cancelID],
    });
    for (const { name, id } of [
      { name: agentName1, id: agent1 },
      { name: agentName2, id: agent2 },
    ]) {
      await adminPage.goto(`${webURL}/agents`);
      const card = adminPage.locator('.ant-card').filter({ hasText: name });
      const deleteResponse = waitForMutation(adminPage, `/agents/${id}`, 'DELETE');
      await card.getByRole('button', { name: '删除 Agent' }).click();
      await adminPage.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
      expect((await deleteResponse).status()).toBe(200);
    }
    recordEvidence(evidence, 'collab agent cleanup');
  } finally {
    await page.close();
    await adminPage.close();
  }
  return completed;
};
