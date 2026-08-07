import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import { restoreActorSession, type BrowserActor } from '../core/actors';
import {
  addGeneratedActorMembership, configureManagedModels, requireUUID, withTenantQuery, type DatabasePool,
} from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface OperationGatePackContext {
  actor: BrowserActor;      // memberA：发起自修改（提案 + 重放消费）
  adminActor: BrowserActor; // tenantAdmin：审批 + agent 生命周期
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  fixtureURL: string;
  backendURL: string;
}

const waitForMutation = (page: Page, path: string | RegExp, method: string) => page.waitForResponse((response) => {
  const urlPath = new URL(response.url()).pathname;
  const pathMatch = typeof path === 'string' ? urlPath === path : path.test(urlPath);
  return pathMatch && response.request().method() === method;
});
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};
// Modal.confirm 的确认按钮可能和 Drawer 内同名按钮共存，必须限定在 confirm 弹层内。
// AntD autoInsertSpace 会给恰好两个汉字的实心按钮文本插空格（批准→"批 准"），
// 故按字符构造 /\s*/ 正则容忍空格（对齐项目 /删\s*除/、/确\s*定/ 先例）
const clickModalConfirmOK = (page: Page, name: string) => page.locator('.ant-modal-confirm')
  .getByRole('button', { name: new RegExp(name.split('').join('\\s*')) }).click();

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
 * OperationGate 审批门禁全链路：member 发起自修改（恒提案 202）→ admin
 * 查看/开始审批/批准（approved + expires_at）→ member 相同载荷重放（单次
 * 消费落地 200 + proposal executed + agent 更新）→ 新内容再次发起（202）→
 * admin 带原因拒绝（rejected + review_note）。proposal 作为审计数据保留。
 */
export const executeOperationGatePack = async ({ actor, adminActor, pool, evidence, webURL, fixtureURL, backendURL }: OperationGatePackContext): Promise<string[]> => {
  // #281 后每个 guest 持有独立沙箱租户：跨 actor fixture 必须落在 admin 租户，
  // 否则 memberA 看不到 tenantAdmin 创建的 agent。
  const tenantID = requireUUID(adminActor.tenantID ?? '', 'tenant_id');
  const proposerID = requireUUID(actor.userID ?? '', 'user_id');
  const reviewerID = requireUUID(adminActor.userID ?? '', 'admin_user_id');
  const completed: string[] = [];
  const memberPage = await actor.context.newPage();
  const adminPage = await adminActor.context.newPage();
  const agentName = `E2E-GateAgent-${Date.now()}`;
  let agentID = '';
  let firstProposalID = '';
  let secondProposalID = '';
  try {
    await configureManagedModels(pool, tenantID, fixtureURL, adminActor.accessToken ?? '', backendURL);

    // ── 0. #281 适配：memberA 加入 admin 租户并换发 member claim 会话 ───────
    // 幂等 SQL 落成员行（soak 重跑安全）+ 真实 switch-tenant 换发 JWT；owner
    // claim 的会话会显示管理按钮，看不到"发起自修改"入口，故必须在 member
    // 角色下发 self-modify 提案。
    await addGeneratedActorMembership(pool, tenantID, proposerID, 'member');
    actor.tenantID = tenantID;
    await restoreActorSession(actor, backendURL);

    // ── 1. 管理员创建 E2E agent（模型目录是 admin 路由，member 无法创建） ──
    agentID = await createAgentViaUI(adminPage, webURL, agentName);

    // ── 2. member 发起自修改 → 恒提案 202 ────────────────────────────────────
    await memberPage.goto(`${webURL}/agents`);
    const memberCard = memberPage.locator('.ant-card').filter({ hasText: agentName });
    await expect(memberCard.getByRole('button', { name: '发起自修改' })).toBeVisible();
    await memberCard.getByRole('button', { name: '发起自修改' }).click();
    await expect(memberPage.locator('.ant-modal-content')).toContainText('发起自修改');
    // 描述改为与 agent 当前值不同的值，制造可落地的差异
    // 预填契约：modal 打开即应显示 agent 当前值（useLayoutEffect 同步预填）
    await expect(memberPage.getByLabel('名称')).toHaveValue(agentName);
    await memberPage.getByLabel('描述').fill('全系统 stateful 验收，待审批修改');
    const firstResponse = waitForMutation(memberPage, `/agents/${agentID}/self-modify`, 'POST');
    await memberPage.locator('.ant-modal-content').getByRole('button', { name: '提交审批' }).click();
    const first = await firstResponse;
    expect(first.status()).toBe(202);
    const firstBody = await first.json() as { status?: string; proposalId?: string };
    expect(firstBody.status).toBe('pending_approval');
    firstProposalID = requireUUID(firstBody.proposalId ?? '', 'operation_proposal_id');
    // DB：proposed + 提案人 + 服务端指纹 + 去敏摘要（Go struct 无 json tag → PascalCase 键）
    expect((await rows<{ status: string; proposer_id: string; fingerprint: string; payload_summary: Record<string, unknown> }>(
      pool, tenantID,
      'SELECT status, proposer_id, fingerprint, payload_summary FROM operation_proposals WHERE id=$1',
      [firstProposalID]))[0])
      .toEqual(expect.objectContaining({
        status: 'proposed',
        proposer_id: proposerID,
        fingerprint: expect.stringContaining('operation-gate:v1:sha256:'),
        payload_summary: expect.objectContaining({ Description: '全系统 stateful 验收，待审批修改' }),
      }));
    completed.push('agent.mutation.post.agents.id.self.modify');
    recordEvidence(evidence, 'self-modify pending proposal');

    // ── 3. 管理员查看待审批列表 ──────────────────────────────────────────────
    const listResponse = waitForMutation(adminPage, '/operation-proposals', 'GET');
    await adminPage.goto(`${webURL}/operation-proposals`);
    expect((await listResponse).status()).toBe(200);
    await expect(adminPage.getByRole('heading', { name: '操作审批' })).toBeVisible();
    completed.push('operation-gate.route.operation.proposals');

    // ── 4. 查看详情（去敏 diff）→ 开始审批 → reviewing ───────────────────────
    const proposalRow = adminPage.locator('tr').filter({ hasText: agentID });
    await expect(proposalRow).toBeVisible();
    await expect(proposalRow).toContainText('自修改');
    await expect(proposalRow).toContainText('待审批');
    const detailResponse = waitForMutation(adminPage, `/operation-proposals/${firstProposalID}`, 'GET');
    await proposalRow.getByRole('button', { name: '查看' }).click();
    expect((await detailResponse).status()).toBe(200);
    await expect(adminPage.getByText('变更内容（已脱敏）')).toBeVisible();
    await expect(adminPage.locator('pre')).toContainText('待审批修改');
    const reviewResponse = waitForMutation(adminPage, `/operation-proposals/${firstProposalID}/review`, 'POST');
    await adminPage.getByRole('button', { name: '开始审批' }).click();
    expect((await reviewResponse).status()).toBe(200);
    // 批准/拒绝是实心 2 字按钮：AntD autoInsertSpace 插空格 → 正则容忍
    await expect(adminPage.getByRole('button', { name: /批\s*准/ })).toBeVisible();
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM operation_proposals WHERE id=$1', [firstProposalID]))[0]?.status).toBe('reviewing');
    completed.push('operation-gate.mutation.post.operation.proposals.id.review');
    recordEvidence(evidence, 'proposal review started');

    // ── 5. 批准 → approved + expires_at + 审批人 ─────────────────────────────
    const approveResponse = waitForMutation(adminPage, `/operation-proposals/${firstProposalID}/approve`, 'POST');
    await adminPage.getByRole('button', { name: /批\s*准/ }).click();
    await clickModalConfirmOK(adminPage, '批准');
    expect((await approveResponse).status()).toBe(200);
    const approvedRow = (await rows<{ status: string; reviewed_by: string; expires_at: Date | null }>(
      pool, tenantID,
      'SELECT status, reviewed_by, expires_at FROM operation_proposals WHERE id=$1', [firstProposalID]))[0];
    expect(approvedRow).toBeTruthy();
    expect(approvedRow!.status).toBe('approved');
    expect(approvedRow!.reviewed_by).toBe(reviewerID);
    expect(approvedRow!.expires_at).toBeTruthy();
    completed.push('operation-gate.mutation.post.operation.proposals.id.approve');
    recordEvidence(evidence, 'proposal approved');

    // ── 6. member 以相同载荷重放 → 单次消费落地 200 ─────────────────────────
    await memberPage.goto(`${webURL}/agents`);
    const replayCard = memberPage.locator('.ant-card').filter({ hasText: agentName });
    await replayCard.getByRole('button', { name: '发起自修改' }).click();
    // 表单预填 = agent 当前值（未变）；把描述填回第一次提交的 P1 值 → 载荷一致 → 同指纹消费
    await memberPage.getByLabel('描述').fill('全系统 stateful 验收，待审批修改');
    const replayResponse = waitForMutation(memberPage, `/agents/${agentID}/self-modify`, 'POST');
    await memberPage.locator('.ant-modal-content').getByRole('button', { name: '提交审批' }).click();
    const replayed = await replayResponse;
    expect(replayed.status()).toBe(200);
    const replayBody = await replayed.json() as { status?: string };
    expect(replayBody.status).toBe('approved');
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM operation_proposals WHERE id=$1', [firstProposalID]))[0]?.status).toBe('executed');
    expect((await rows<{ description: string }>(pool, tenantID,
      'SELECT description FROM agents WHERE id=$1', [agentID]))[0]?.description)
      .toBe('全系统 stateful 验收，待审批修改');
    recordEvidence(evidence, 'approved replay applied');

    // ── 7. member 修改为新内容 → 新指纹 → 恒提案 202 ─────────────────────────
    await memberPage.goto(`${webURL}/agents`);
    const secondCard = memberPage.locator('.ant-card').filter({ hasText: agentName });
    await secondCard.getByRole('button', { name: '发起自修改' }).click();
    await memberPage.getByLabel('描述').fill('全系统 stateful 验收，拒绝测试');
    const secondResponse = waitForMutation(memberPage, `/agents/${agentID}/self-modify`, 'POST');
    await memberPage.locator('.ant-modal-content').getByRole('button', { name: '提交审批' }).click();
    const second = await secondResponse;
    expect(second.status()).toBe(202);
    const secondBody = await second.json() as { status?: string; proposalId?: string };
    expect(secondBody.status).toBe('pending_approval');
    secondProposalID = requireUUID(secondBody.proposalId ?? '', 'operation_proposal_id');
    expect(secondProposalID).not.toBe(firstProposalID);

    // ── 8. 管理员拒绝（必填原因）→ rejected + review_note ────────────────────
    await adminPage.goto(`${webURL}/operation-proposals`);
    // 第一个 proposal 已 executed，不在待审列表；列表仅剩新提案
    const secondRow = adminPage.locator('tr').filter({ hasText: agentID });
    await expect(secondRow).toBeVisible();
    await expect(secondRow).toContainText('待审批');
    await secondRow.getByRole('button', { name: '查看' }).click();
    await adminPage.getByRole('button', { name: '开始审批' }).click();
    await expect(adminPage.getByRole('button', { name: /拒\s*绝/ })).toBeVisible();
    await adminPage.getByPlaceholder('拒绝时必填原因（最多 500 字）').fill('E2E stateful 拒绝验证');
    const rejectResponse = waitForMutation(adminPage, `/operation-proposals/${secondProposalID}/reject`, 'POST');
    await adminPage.getByRole('button', { name: /拒\s*绝/ }).click();
    await clickModalConfirmOK(adminPage, '拒绝');
    expect((await rejectResponse).status()).toBe(200);
    const rejectedRow = (await rows<{ status: string; review_note: string; reviewed_by: string }>(
      pool, tenantID,
      'SELECT status, review_note, reviewed_by FROM operation_proposals WHERE id=$1', [secondProposalID]))[0];
    expect(rejectedRow).toBeTruthy();
    expect(rejectedRow!.status).toBe('rejected');
    expect(rejectedRow!.review_note).toBe('E2E stateful 拒绝验证');
    expect(rejectedRow!.reviewed_by).toBe(reviewerID);
    completed.push('operation-gate.mutation.post.operation.proposals.id.reject');
    recordEvidence(evidence, 'proposal rejected with note');

    // ── 9. 管理员清理 E2E agent（proposal 作为审计数据保留） ─────────────────
    await adminPage.goto(`${webURL}/agents`);
    const deleteCard = adminPage.locator('.ant-card').filter({ hasText: agentName });
    const deleteResponse = waitForMutation(adminPage, `/agents/${agentID}`, 'DELETE');
    await deleteCard.getByRole('button', { name: '删除 Agent' }).click();
    await adminPage.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteResponse).status()).toBe(200);
    recordEvidence(evidence, 'gate agent cleanup');
  } finally {
    await memberPage.close();
    await adminPage.close();
  }
  return completed;
};
