import { randomUUID } from 'node:crypto';

import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantMutation, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface WorkflowPackContext {
  actor: BrowserActor;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
}

const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));

const tenantRows = async <R extends QueryResultRow>(
  pool: DatabasePool,
  tenantID: string,
  text: string,
  values: unknown[],
): Promise<R[]> => (await withTenantQuery<R>(pool, tenantID, { text, values })).rows;

const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};

const seedRun = async (
  pool: DatabasePool,
  tenantID: string,
  actorID: string,
  definitionID: string,
  versionID: string,
  status: 'running' | 'paused' | 'manual_intervention',
): Promise<{ runID: string; attemptID?: string; effectID?: string }> => {
  const runID = randomUUID();
  const attemptID = status === 'manual_intervention' ? randomUUID() : undefined;
  const effectID = status === 'manual_intervention' ? randomUUID() : undefined;
  const node = {
    id: 'approval-seeded', name: '状态控制步骤', type: 'approval', agent_id: '',
    input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
  };
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO workflow_runs
      (id,definition_id,version_id,version_no,status,generation,snapshot_json,input_json,idempotency_key,request_hash,created_by,started_at)
      VALUES ($1,$2,$3,1,$4,1,$5,$6,$7,$8,$9,now())`,
    values: [runID, definitionID, versionID, status, JSON.stringify({ nodes: [node], edges: [], max_concurrency: 0 }),
      JSON.stringify({ task: 'stateful control' }), `e2e-${runID}`, `hash-${runID}`, actorID],
  });
  if (attemptID && effectID) {
    await withTenantMutation(pool, tenantID, {
      text: `INSERT INTO workflow_node_attempts
        (id,run_id,node_id,attempt_no,status,run_generation,effect_class,input_text)
        VALUES ($1,$2,$3,1,'manual_intervention',1,'non_idempotent','stateful control')`,
      values: [attemptID, runID, node.id],
    });
    await withTenantMutation(pool, tenantID, {
      text: `INSERT INTO workflow_effect_intents
        (id,run_id,node_id,attempt_id,run_generation,effect_class,idempotency_key,status,reason)
        VALUES ($1,$2,$3,$4,1,'non_idempotent',$5,'unknown','外部结果待确认')`,
      values: [effectID, runID, node.id, attemptID, `e2e-effect-${effectID}`],
    });
  }
  return { runID, attemptID, effectID };
};

export const executeWorkflowPack = async ({
  actor, pool, evidence, webURL,
}: WorkflowPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const actorID = requireUUID(actor.userID ?? '', 'user_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const workflowName = `E2E-Workflow-${Date.now()}`;
  let definitionID = '';
  let versionID = '';
  try {
    const catalogResponse = waitForMutation(page, '/workflows', 'GET');
    await page.goto(`${webURL}/workflows`);
    expect((await catalogResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '工作流' })).toBeVisible();
    completed.push('workflow.route.workflows');

    await page.getByRole('button', { name: '新建工作流' }).click();
    await expect(page).toHaveURL(`${webURL}/workflows/new`);
    await expect(page.getByRole('region', { name: '工作流画布' })).toBeVisible();
    completed.push('workflow.route.workflows.new');
    await page.getByLabel('工作流名称').fill(workflowName);
    await page.getByLabel('工作流说明').fill('全系统 stateful 浏览器验收');
    await page.getByLabel('任务名称').fill('审批事项');
    await page.getByRole('button', { name: '添加人工审批节点' }).click();
    await page.getByRole('textbox', { name: '节点名称', exact: true }).fill('管理员审批');
    const createResponse = waitForMutation(page, '/workflows', 'POST');
    await page.getByRole('button', { name: '保存草稿' }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    const createdBody = await created.json() as { id: string; revision: number };
    definitionID = requireUUID(createdBody.id, 'workflow_definition_id');
    await expect(page).toHaveURL(`${webURL}/workflows/${definitionID}/edit`);
    completed.push('workflow.mutation.post.workflows', 'workflow.route.workflows.id.edit');
    recordEvidence(evidence, 'workflow draft creation');

    // 编辑页表单在定义 GET 完成后才异步水合（setFieldsValue 回填）。先等旧值出现，
    // 确保水合完成再修改，避免保存时读到水合前的空值。
    await expect(page.getByLabel('工作流说明')).toHaveValue('全系统 stateful 浏览器验收');
    // 受控 TextArea 对 locator.fill() 的一次性 value 写入会闪回：DOM 值变了但 React
    // store 未同步，后续渲染用旧值重置，导致保存的草稿 description 仍是旧值。改用
    // 真实键盘输入（Ctrl+A 全选 + 逐字符），与真实用户逐字符输入一致，onChange→
    // onValuesChange→designer.setDescription 逐次同步，值不会被覆盖。
    const descriptionInput = page.getByLabel('工作流说明');
    await descriptionInput.click();
    await descriptionInput.press('Control+A');
    await descriptionInput.pressSequentially('全系统 stateful 浏览器验收，已修改');
    const updateResponse = waitForMutation(page, `/workflows/${definitionID}/draft`, 'PUT');
    await page.getByRole('button', { name: '保存草稿' }).click();
    expect((await updateResponse).status()).toBe(200);
    const draftRows = await tenantRows<{ draft_revision: string; description: string }>(pool, tenantID,
      'SELECT draft_revision::text, description FROM workflow_definitions WHERE id=$1', [definitionID]);
    expect(draftRows).toEqual([{ draft_revision: '2', description: '全系统 stateful 浏览器验收，已修改' }]);
    completed.push('workflow.mutation.put.workflows.workflowid.draft');

    const validateResponse = waitForMutation(page, `/workflows/${definitionID}/validate`, 'POST');
    await page.getByRole('button', { name: '校验工作流' }).click();
    expect((await validateResponse).status()).toBe(200);
    await expect(page.getByText('校验通过', { exact: true })).toBeVisible();
    completed.push('workflow.mutation.post.workflows.workflowid.validate');

    const publishResponse = waitForMutation(page, `/workflows/${definitionID}/publish`, 'POST');
    await page.getByRole('button', { name: '发布工作流' }).click();
    await page.getByRole('button', { name: '确认发布' }).click();
    const published = await publishResponse;
    expect(published.status()).toBe(201);
    versionID = requireUUID((await published.json() as { id: string }).id, 'workflow_version_id');
    await expect(page).toHaveURL(`${webURL}/workflows/${definitionID}/versions/${versionID}`);
    await expect(page.getByRole('region', { name: '工作流版本图' })).toBeVisible();
    completed.push('workflow.mutation.post.workflows.workflowid.publish', 'workflow.route.workflows.id.versions.versionid');
    recordEvidence(evidence, 'workflow update validation and publication');

    await page.goto(`${webURL}/workflows/${definitionID}/run`);
    await expect(page.getByRole('button', { name: '开始运行' })).toBeVisible();
    completed.push('workflow.route.workflows.id.run');
    await page.getByLabel('审批事项').fill('请批准本轮 stateful 验收');
    const startResponse = waitForMutation(page, '/workflow-runs', 'POST');
    await page.getByRole('button', { name: '开始运行' }).click();
    const started = await startResponse;
    expect(started.status()).toBe(202);
    const runID = requireUUID((await started.json() as { run_id: string }).run_id, 'workflow_run_id');
    await expect(page).toHaveURL(`${webURL}/workflow-runs/${runID}`);
    completed.push('workflow.mutation.post.workflow.runs', 'workflow.route.workflow.runs.runid');
    await expect(page.getByText('管理员审批')).toBeVisible();
    const approvalCard = page.locator('.ant-card').filter({ hasText: '等待审批' });
    await expect(approvalCard).toBeVisible();
    await approvalCard.getByLabel('审批意见').fill('stateful acceptance approved');
    const approvalResponse = page.waitForResponse((response) => (
      /\/workflow-approvals\/[^/]+\/decision$/.test(new URL(response.url()).pathname)
      && response.request().method() === 'POST'
    ));
    await approvalCard.getByRole('button', { name: /批\s*准/ }).click();
    await page.getByRole('button', { name: '确 定' }).click();
    expect((await approvalResponse).status()).toBe(200);
    const approvalRows = await tenantRows<{ status: string; decision_comment: string }>(pool, tenantID,
      'SELECT status,decision_comment FROM workflow_approvals WHERE run_id=$1', [runID]);
    expect(approvalRows).toEqual([{ status: 'approved', decision_comment: 'stateful acceptance approved' }]);
    completed.push('workflow.mutation.post.workflow.approvals.approvalid.decision');
    recordEvidence(evidence, 'workflow run and approval');

    await page.goto(`${webURL}/workflow-runs`);
    await expect(page.getByRole('heading', { name: '运行中心' })).toBeVisible();
    completed.push('workflow.route.workflow.runs');

    const pauseRun = await seedRun(pool, tenantID, actorID, definitionID, versionID, 'running');
    await page.goto(`${webURL}/workflow-runs/${pauseRun.runID}`);
    const pauseResponse = waitForMutation(page, `/workflow-runs/${pauseRun.runID}/pause`, 'POST');
    await page.getByRole('button', { name: /暂\s*停/ }).click();
    await page.getByRole('button', { name: /确\s*认/ }).click();
    expect((await pauseResponse).status()).toBe(202);
    const paused = await tenantRows<{ status: string }>(pool, tenantID,
      'SELECT status FROM workflow_runs WHERE id=$1', [pauseRun.runID]);
    expect(['pause_requested', 'paused']).toContain(paused[0].status);
    completed.push('workflow.mutation.post.workflow.runs.runid.pause');

    const resumeRun = await seedRun(pool, tenantID, actorID, definitionID, versionID, 'paused');
    await page.goto(`${webURL}/workflow-runs/${resumeRun.runID}`);
    const resumeResponse = waitForMutation(page, `/workflow-runs/${resumeRun.runID}/resume`, 'POST');
    await page.getByRole('button', { name: /继\s*续/ }).click();
    await page.getByRole('button', { name: /确\s*认/ }).click();
    expect((await resumeResponse).status()).toBe(202);
    const resumed = await tenantRows<{ status: string }>(pool, tenantID,
      'SELECT status FROM workflow_runs WHERE id=$1', [resumeRun.runID]);
    expect(['queued', 'running', 'completed']).toContain(resumed[0].status);
    completed.push('workflow.mutation.post.workflow.runs.runid.resume');

    const cancelRun = await seedRun(pool, tenantID, actorID, definitionID, versionID, 'running');
    await page.goto(`${webURL}/workflow-runs/${cancelRun.runID}`);
    const cancelResponse = waitForMutation(page, `/workflow-runs/${cancelRun.runID}/cancel`, 'POST');
    await page.getByRole('button', { name: '取消运行' }).click();
    await page.getByRole('button', { name: /确\s*认/ }).click();
    expect((await cancelResponse).status()).toBe(202);
    const canceled = await tenantRows<{ status: string }>(pool, tenantID,
      'SELECT status FROM workflow_runs WHERE id=$1', [cancelRun.runID]);
    expect(['cancel_requested', 'canceled']).toContain(canceled[0].status);
    completed.push('workflow.mutation.post.workflow.runs.runid.cancel');
    recordEvidence(evidence, 'workflow pause resume and cancel controls');

    const manualRun = await seedRun(pool, tenantID, actorID, definitionID, versionID, 'manual_intervention');
    await page.goto(`${webURL}/workflow-runs/${manualRun.runID}`);
    const manualCard = page.locator('.ant-card').filter({ hasText: '需要人工处置' });
    await expect(manualCard).toBeVisible();
    await manualCard.getByLabel('处置结果摘要').fill('外部结果已人工确认');
    const manualPath = `/workflow-runs/${manualRun.runID}/manual-interventions/${manualRun.effectID}/resolve`;
    const resolveRequest = page.waitForRequest((request) => (
      new URL(request.url()).pathname === manualPath && request.method() === 'POST'
    ));
    await manualCard.getByRole('button', { name: '标记成功' }).click();
    const manualConfirmation = page.locator('.ant-modal-confirm').filter({ hasText: '确认人工处置？' });
    await expect(manualConfirmation).toBeVisible();
    await manualConfirmation.locator('.ant-btn-primary').click();
    const sentResolution = await resolveRequest;
    const resolveResponse = await sentResolution.response();
    expect(resolveResponse?.status()).toBe(200);
    const effectRows = await tenantRows<{ status: string; output_summary: string }>(pool, tenantID,
      'SELECT status,output_summary FROM workflow_effect_intents WHERE id=$1', [manualRun.effectID]);
    expect(effectRows).toEqual([{ status: 'succeeded', output_summary: '外部结果已人工确认' }]);
    completed.push('workflow.mutation.post.workflow.runs.runid.manual.interventions.effectid.resolve');
    recordEvidence(evidence, 'workflow manual intervention');

    await page.goto(`${webURL}/workflows/new`);
    const disposableName = `E2E-Disposable-${Date.now()}`;
    await page.getByLabel('工作流名称').fill(disposableName);
    await page.getByLabel('任务名称').fill('待删除任务');
    await page.getByRole('button', { name: '添加人工审批节点' }).click();
    const disposableCreate = waitForMutation(page, '/workflows', 'POST');
    await page.getByRole('button', { name: '保存草稿' }).click();
    const disposableID = requireUUID((await (await disposableCreate).json() as { id: string }).id, 'disposable_workflow_id');
    await page.goto(`${webURL}/workflows`);
    const row = page.locator('tr').filter({ hasText: disposableName });
    await expect(row).toBeVisible();
    const deleteResponse = waitForMutation(page, `/workflows/${disposableID}`, 'DELETE');
    await row.getByRole('button', { name: '删除草稿' }).click();
    await page.getByRole('button', { name: '确认删除' }).click();
    expect((await deleteResponse).status()).toBe(200);
    const deleted = await tenantRows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM workflow_definitions WHERE id=$1', [disposableID]);
    expect(deleted).toEqual([{ count: '0' }]);
    completed.push('workflow.mutation.delete.workflows.workflowid');
    recordEvidence(evidence, 'unused workflow draft deletion');
  } finally {
    await page.close();
  }
  return completed;
};
