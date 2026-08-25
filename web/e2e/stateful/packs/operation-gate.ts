import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import { restoreActorSession, type BrowserActor } from '../core/actors';
import {
  addGeneratedActorMembership, configureManagedModels, requireUUID, withTenantQuery, type DatabasePool,
} from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface OperationGatePackContext {
  actor: BrowserActor;      // memberA：申请编辑权限 + 发起自修改（提案 + 重放消费）
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
// 故按字符构造 /\s*/ 正则容忍空格（对齐项目 /删\s*除/、/确\s*定/ 先例）。
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

// request-editor 202 只回 {"status":"pending_approval"}（无 proposalId），
// 按 grant 指纹 grant_editor|agent|<id>|<actor> 反查最新提案 id（拒绝后指纹
// 释放可再次申请，故须同时限定 status）。
const grantProposalIdByStatus = async (pool: DatabasePool, tenantID: string, agentID: string, proposerID: string, status: string): Promise<string> => {
  const grantRows = await rows<{ id: string }>(pool, tenantID,
    'SELECT id FROM operation_proposals WHERE fingerprint=$1 AND status=$2 ORDER BY created_at DESC LIMIT 1',
    [`grant_editor|agent|${agentID}|${proposerID}`, status]);
  return requireUUID(grantRows[0]?.id ?? '', 'operation_proposal_id');
};

/**
 * OperationGate 审批门禁全链路（P3 grant_editor 申请流 + 既有 gated self-modify）：
 * 1) member 在 agent 编辑页「申请编辑权限」→ grant_editor 提案 202（无 proposalId，
 *    按指纹反查）→ admin 查看/拒绝（rejected + review_note）。
 * 2) member 直接 API 发起 self-modify 恒提案 202 → admin 开始审批（reviewing）→
 *    批准（approved + expires_at）→ member 相同载荷重放（单次消费 200 + executed
 *    + agent 更新）。
 * 3) member 再次申请编辑权限 → admin 批准（grant 白名单直接生效 → 提案 executed）
 *    → member 刷新编辑页出现「保存修改」（编辑权已授予）。
 * proposal 作为审计数据保留。
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
  try {
    await configureManagedModels(pool, tenantID, fixtureURL, adminActor.accessToken ?? '', backendURL);

    // ── 0. #281 适配：memberA 加入 admin 租户并换发 member claim 会话 ───────
    // 幂等 SQL 落成员行（soak 重跑安全）+ 真实 switch-tenant 换发 JWT；owner
    // claim 的会话不会呈现"申请编辑权限"入口，必须在 member 角色下发起申请。
    await addGeneratedActorMembership(pool, tenantID, proposerID, 'member');
    actor.tenantID = tenantID;
    await restoreActorSession(actor, backendURL);

    // ── 1. 管理员创建 E2E agent（模型目录是 admin 路由，member 无法创建） ──
    agentID = await createAgentViaUI(adminPage, webURL, agentName);

    // ── 2. member 申请编辑权限 → grant_editor 提案 202 ──────────────────────
    await memberPage.goto(`${webURL}/agents/${agentID}/edit`);
    await expect(memberPage.getByRole('heading', { name: '查看 Agent 配置' })).toBeVisible({ timeout: 15000 });
    const requestEditorResponse = waitForMutation(memberPage, `/agents/${agentID}/request-editor`, 'POST');
    // 按钮带 LockOutlined 图标（AntD icon aria-label 计入可访问名），用正则匹配。
    await memberPage.getByRole('button', { name: /申请编辑权限/ }).click();
    const requestEditor = await requestEditorResponse;
    expect(requestEditor.status()).toBe(202);
    const requestEditorBody = await requestEditor.json() as { status?: string };
    expect(requestEditorBody.status).toBe('pending_approval');
    const grantProposalID = await grantProposalIdByStatus(pool, tenantID, agentID, proposerID, 'proposed');
    completed.push('agent.mutation.post.agents.id.request.editor');
    recordEvidence(evidence, 'grant editor request pending proposal');

    // ── 3. 管理员查看待审批列表（/operation-proposals 重定向到权限审批中心） ──
    const listResponse = waitForMutation(adminPage, '/operation-proposals', 'GET');
    await adminPage.goto(`${webURL}/operation-proposals`);
    expect((await listResponse).status()).toBe(200);
    // M3/M4 审批中心重构：顶层 Tabs + 面板标题，权限 tab 面板标题即「权限审批」。
    await expect(adminPage.getByRole('heading', { name: '权限审批' })).toBeVisible({ timeout: 15000 });
    completed.push('operation-gate.route.operation.proposals');

    // ── 4. 查看 grant 提案详情 → 拒绝（必填原因）→ rejected + review_note ──
    // grant 行资源列展示 resourceName（agent 名），用 agentName 定位。
    const grantRow = adminPage.locator('tr').filter({ hasText: agentName }).first();
    await expect(grantRow).toBeVisible({ timeout: 15000 });
    await expect(grantRow).toContainText('权限申请');
    await expect(grantRow).toContainText('待审批');
    await grantRow.getByRole('button', { name: '查看' }).click();
    await expect(adminPage.getByText('申请/变更内容（已脱敏）')).toBeVisible();
    await expect(adminPage.locator('pre')).toContainText(agentName);
    await adminPage.getByPlaceholder('拒绝时必填原因（最多 500 字）').fill('E2E stateful 拒绝权限申请');
    const rejectResponse = waitForMutation(adminPage, `/operation-proposals/${grantProposalID}/reject`, 'POST');
    await adminPage.getByRole('button', { name: /拒\s*绝/ }).click();
    await clickModalConfirmOK(adminPage, '拒绝');
    expect((await rejectResponse).status()).toBe(200);
    const rejectedRow = (await rows<{ status: string; review_note: string; reviewed_by: string }>(
      pool, tenantID,
      'SELECT status, review_note, reviewed_by FROM operation_proposals WHERE id=$1', [grantProposalID]))[0];
    expect(rejectedRow).toBeTruthy();
    expect(rejectedRow!.status).toBe('rejected');
    expect(rejectedRow!.review_note).toBe('E2E stateful 拒绝权限申请');
    expect(rejectedRow!.reviewed_by).toBe(reviewerID);
    completed.push('operation-gate.mutation.post.operation.proposals.id.reject');
    recordEvidence(evidence, 'grant editor proposal rejected with note');

    // ── 5. member 直接 API 发起 self-modify → 恒提案 202 ────────────────────
    // SelfModifyRequest 无 json tag，key=Go 字段名；载荷与服务端指纹计算一致，
    // 重放时必须原样重发。
    const selfModifyDescription = 'E2E 审批后落地内容';
    const selfModifyPayload = { Name: agentName, Description: selfModifyDescription };
    const selfModifyResponse = await actor.context.request.post(
      `${backendURL}/agents/${agentID}/self-modify`,
      { headers: { Authorization: `Bearer ${actor.accessToken ?? ''}` }, data: selfModifyPayload },
    );
    expect(selfModifyResponse.status()).toBe(202);
    const selfModifyBody = await selfModifyResponse.json() as { status?: string; proposalId?: string };
    expect(selfModifyBody.status).toBe('pending_approval');
    const selfModifyProposalID = requireUUID(selfModifyBody.proposalId ?? '', 'operation_proposal_id');
    expect((await rows<{ status: string; fingerprint: string; payload_summary: Record<string, unknown> }>(
      pool, tenantID,
      'SELECT status, fingerprint, payload_summary FROM operation_proposals WHERE id=$1',
      [selfModifyProposalID]))[0])
      .toEqual(expect.objectContaining({
        status: 'proposed',
        fingerprint: expect.stringContaining('operation-gate:v1:sha256:'),
        payload_summary: expect.objectContaining({ Description: selfModifyDescription }),
      }));

    // ── 6. 管理员开始审批 → reviewing ───────────────────────────────────────
    // self-modify 行资源列回退到 agentId，用 agentID 定位；非 grant 提案需先「开始审批」。
    await adminPage.goto(`${webURL}/operation-proposals`);
    const selfModifyRow = adminPage.locator('tr').filter({ hasText: agentID }).first();
    await expect(selfModifyRow).toBeVisible({ timeout: 15000 });
    await expect(selfModifyRow).toContainText('自修改');
    await expect(selfModifyRow).toContainText('待审批');
    await selfModifyRow.getByRole('button', { name: '查看' }).click();
    await expect(adminPage.getByText('申请/变更内容（已脱敏）')).toBeVisible();
    await expect(adminPage.locator('pre')).toContainText(selfModifyDescription);
    const reviewResponse = waitForMutation(adminPage, `/operation-proposals/${selfModifyProposalID}/review`, 'POST');
    await adminPage.getByRole('button', { name: '开始审批' }).click();
    expect((await reviewResponse).status()).toBe(200);
    // handleReview 会重开抽屉（openDetail）刷新到 reviewing 态，此时才出现批准/拒绝。
    await expect(adminPage.getByRole('button', { name: /批\s*准/ })).toBeVisible();
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM operation_proposals WHERE id=$1', [selfModifyProposalID]))[0]?.status).toBe('reviewing');
    completed.push('operation-gate.mutation.post.operation.proposals.id.review');
    recordEvidence(evidence, 'self-modify proposal review started');

    // ── 7. 批准 → approved + expires_at + 审批人 ─────────────────────────────
    const approveResponse = waitForMutation(adminPage, `/operation-proposals/${selfModifyProposalID}/approve`, 'POST');
    await adminPage.getByRole('button', { name: /批\s*准/ }).click();
    await clickModalConfirmOK(adminPage, '批准');
    expect((await approveResponse).status()).toBe(200);
    const approvedRow = (await rows<{ status: string; reviewed_by: string; expires_at: Date | null }>(
      pool, tenantID,
      'SELECT status, reviewed_by, expires_at FROM operation_proposals WHERE id=$1', [selfModifyProposalID]))[0];
    expect(approvedRow).toBeTruthy();
    expect(approvedRow!.status).toBe('approved');
    expect(approvedRow!.reviewed_by).toBe(reviewerID);
    expect(approvedRow!.expires_at).toBeTruthy();
    completed.push('operation-gate.mutation.post.operation.proposals.id.approve');
    recordEvidence(evidence, 'self-modify proposal approved');

    // ── 8. member 相同载荷重放 → 单次消费 200 + executed + agent 更新 ───────
    const replayResponse = await actor.context.request.post(
      `${backendURL}/agents/${agentID}/self-modify`,
      { headers: { Authorization: `Bearer ${actor.accessToken ?? ''}` }, data: selfModifyPayload },
    );
    expect(replayResponse.status()).toBe(200);
    const replayBody = await replayResponse.json() as { status?: string };
    expect(replayBody.status).toBe('approved');
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM operation_proposals WHERE id=$1', [selfModifyProposalID]))[0]?.status).toBe('executed');
    expect((await rows<{ description: string }>(pool, tenantID,
      'SELECT description FROM agents WHERE id=$1', [agentID]))[0]?.description).toBe(selfModifyDescription);
    completed.push('agent.mutation.post.agents.id.self.modify');
    recordEvidence(evidence, 'approved replay applied');

    // ── 9. member 再次申请编辑权限 → admin 批准（grant 白名单直接生效）─────
    await memberPage.goto(`${webURL}/agents/${agentID}/edit`);
    await expect(memberPage.getByRole('button', { name: /申请编辑权限/ })).toBeVisible({ timeout: 15000 });
    const secondRequestResponse = waitForMutation(memberPage, `/agents/${agentID}/request-editor`, 'POST');
    await memberPage.getByRole('button', { name: /申请编辑权限/ }).click();
    expect((await secondRequestResponse).status()).toBe(202);
    const secondGrantID = await grantProposalIdByStatus(pool, tenantID, agentID, proposerID, 'proposed');
    await adminPage.goto(`${webURL}/operation-proposals`);
    const secondGrantRow = adminPage.locator('tr').filter({ hasText: agentName }).first();
    await expect(secondGrantRow).toBeVisible({ timeout: 15000 });
    await secondGrantRow.getByRole('button', { name: '查看' }).click();
    // grant_editor 在 proposed 态直接呈现批准/拒绝（无「开始审批」）。
    const approveGrantResponse = waitForMutation(adminPage, `/operation-proposals/${secondGrantID}/approve`, 'POST');
    await adminPage.getByRole('button', { name: /批\s*准/ }).click();
    await clickModalConfirmOK(adminPage, '批准');
    expect((await approveGrantResponse).status()).toBe(200);
    expect((await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM operation_proposals WHERE id=$1', [secondGrantID]))[0]?.status).toBe('executed');
    recordEvidence(evidence, 'grant editor proposal approved and whitelist granted');

    // ── 10. member 刷新编辑页 → 已授予编辑权（出现「保存修改」） ─────────────
    await memberPage.goto(`${webURL}/agents/${agentID}/edit`);
    await expect(memberPage.getByRole('heading', { name: '编辑 Agent' })).toBeVisible({ timeout: 15000 });
    await expect(memberPage.getByRole('button', { name: '保存修改' })).toBeVisible({ timeout: 15000 });
    recordEvidence(evidence, 'member granted editor whitelist');

    // ── 11. 管理员清理 E2E agent（proposal 作为审计数据保留） ───────────────
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
