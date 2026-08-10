import { randomUUID } from 'node:crypto';

import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import { restoreActorSession, type BrowserActor } from '../core/actors';
import { requireUUID, withTenantMutation, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { acceptanceErrors } from '../core/errors';

interface ScheduledTaskPackContext {
  actor: BrowserActor; // tenantAdmin：admin 创建/编辑/启停/删除
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  backendURL: string;
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
// 种子：直插 workflow definition + published version，供级联下拉选择。
// 名称唯一（workflow_definitions.name 有 UNIQUE 约束），soak 每轮复用幂等。
const seedWorkflow = async (pool: DatabasePool, tenantID: string): Promise<{ definitionID: string; versionID: string; name: string }> => {
  const definitionID = randomUUID();
  const versionID = randomUUID();
  const name = `E2E-Scheduled-${Date.now()}`;
  const spec = JSON.stringify({ nodes: [], edges: [], max_concurrency: 1 });
  // 字段类型必须是 workflow 域常量之一（short_text/long_text/...），type:'text' 不存在，
  // 会被 ValidateRunInput 判定为格式不正确 → 创建 400。task 是保留键，schema 不得声明。
  const schema = JSON.stringify({
    task_label: '任务',
    fields: [{ key: 'topic', label: '主题', type: 'short_text', required: true }],
  });
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO workflow_definitions (id, name, description, draft_revision, draft_spec_json, draft_input_schema_json)
      VALUES ($1, $2, '', 1, $3, $4)`,
    values: [definitionID, name, spec, schema],
  });
  await withTenantMutation(pool, tenantID, {
    text: `INSERT INTO workflow_versions (id, definition_id, version_no, name, description, spec_json, input_schema_json)
      VALUES ($1, $2, 1, $3, '', $4, $5)`,
    values: [versionID, definitionID, 'v1', spec, schema],
  });
  return { definitionID, versionID, name };
};

const cleanupWorkflow = async (pool: DatabasePool, tenantID: string, definitionID: string): Promise<void> => {
  await withTenantMutation(pool, tenantID, {
    text: 'DELETE FROM workflow_versions WHERE definition_id=$1',
    values: [definitionID],
  });
  await withTenantMutation(pool, tenantID, {
    text: 'DELETE FROM workflow_definitions WHERE id=$1',
    values: [definitionID],
  });
};

export const executeScheduledTaskPack = async ({
  actor, pool, evidence, webURL, backendURL,
}: ScheduledTaskPackContext): Promise<string[]> => {
  await restoreActorSession(actor, backendURL);
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const taskName = `E2E-Scheduled-Task-${Date.now()}`;
  let seeded: { definitionID: string; versionID: string; name: string } | undefined;
  // 主流程错误与 cleanup 失败聚合上报：cleanup 的残留检查 throw 不能掩盖
  // 主流程的真实失败点（否则只能看到 residual 错误而不知道卡在哪一步）。
  let primaryError: unknown;
  const cleanupTasks = [
    async () => {
      const remaining = await rows<{ count: string }>(pool, tenantID,
        'SELECT count(*)::text AS count FROM scheduled_tasks WHERE name LIKE $1', ['E2E-Scheduled-Task-%']);
      if (remaining[0]?.count !== '0') {
        const leftovers = await rows<{ id: string; name: string; created_at: string; updated_at: string }>(pool, tenantID,
          'SELECT id, name, created_at::text, updated_at::text FROM scheduled_tasks WHERE name LIKE $1', ['E2E-Scheduled-Task-%']);
        throw new Error(`residual scheduled tasks remain: ${remaining[0]?.count} -> ${JSON.stringify(leftovers)}`);
      }
    },
    async () => {
      if (seeded) await cleanupWorkflow(pool, tenantID, seeded.definitionID);
    },
    async () => page.close(),
  ];
  try {
    seeded = await seedWorkflow(pool, tenantID);

    const listResponse = waitForMutation(page, '/scheduled-tasks', 'GET');
    await page.goto(`${webURL}/scheduled-tasks`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '定时任务' })).toBeVisible();
    completed.push('scheduled-task.route.scheduled.tasks');

    await page.getByRole('button', { name: '新建定时任务' }).click();
    await expect(page.getByRole('dialog')).toContainText('新建定时任务');
    await page.getByLabel('名称').fill(taskName);

    // 注意：不能按 role=option 定位——antd virtual 列表的隐藏 a11y 辅助元素
    // （aria-label=名称、children=value）也会匹配 getByRole('option')，且不可点击。
    await page.getByRole('combobox', { name: '选择工作流', exact: true }).click();
    await page.locator('.ant-select-item-option').filter({ hasText: seeded.name }).click();
    await page.getByRole('combobox', { name: '选择工作流版本' }).click();
    await page.locator('.ant-select-item-option').filter({ hasText: /^v1 / }).click();

    await page.getByLabel('Cron 表达式').fill('0 0 * * *');
    await page.getByLabel('输入模板').fill('{"task":"summarize","topic":"weekly"}');
    const createResponse = waitForMutation(page, '/scheduled-tasks', 'POST');
    await page.getByRole('button', { name: '创 建' }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    const taskID = requireUUID((await created.json() as { id: string }).id, 'scheduled_task_id');
    const createdRows = await rows<{ name: string; cron_expr: string; enabled: boolean; next_fire_at: string }>(pool,
      tenantID,
      'SELECT name, cron_expr, enabled, next_fire_at::text FROM scheduled_tasks WHERE id=$1',
      [taskID]);
    expect(createdRows).toHaveLength(1);
    expect(createdRows[0]).toMatchObject({ name: taskName, cron_expr: '0 0 * * *', enabled: true });
    expect(createdRows[0].next_fire_at).not.toBe('');
    completed.push('scheduled-task.mutation.create');
    recordEvidence(evidence, 'scheduled task creation');

    // 编辑：改名后 PUT 全量更新，DB 断言生效。
    // 注意：antd autoInsertSpaceInButton 会把两个汉字的按钮文本渲染成"编 辑"（带空格），
    // name 必须容忍空格；创建/保存按钮同样如此（"创 建"/"保 存"）。
    const row = page.getByRole('row', { name: new RegExp(taskName) });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: /编\s*辑/ }).click();
    await expect(page.getByRole('dialog')).toContainText('编辑定时任务');
    const renamed = `${taskName}-v2`;
    await page.getByLabel('名称').fill(renamed);
    const updateResponse = waitForMutation(page, `/scheduled-tasks/${taskID}`, 'PUT');
    await page.getByRole('button', { name: '保 存' }).click();
    expect((await updateResponse).status()).toBe(200);
    const updatedRows = await rows<{ name: string }>(pool, tenantID,
      'SELECT name FROM scheduled_tasks WHERE id=$1', [taskID]);
    expect(updatedRows).toEqual([{ name: renamed }]);
    completed.push('scheduled-task.mutation.update');
    recordEvidence(evidence, 'scheduled task update');

    // 启停：Switch 关闭再打开，PATCH enabled 两次，DB 断言 enabled 状态。
    const switchRow = page.getByRole('row', { name: new RegExp(renamed) });
    const disableResponse = waitForMutation(page, `/scheduled-tasks/${taskID}/enabled`, 'PATCH');
    await switchRow.getByRole('switch').click();
    expect((await disableResponse).status()).toBe(200);
    const disabledRows = await rows<{ enabled: boolean }>(pool, tenantID,
      'SELECT enabled FROM scheduled_tasks WHERE id=$1', [taskID]);
    expect(disabledRows).toEqual([{ enabled: false }]);
    const enableResponse = waitForMutation(page, `/scheduled-tasks/${taskID}/enabled`, 'PATCH');
    await switchRow.getByRole('switch').click();
    expect((await enableResponse).status()).toBe(200);
    const enabledRows = await rows<{ enabled: boolean }>(pool, tenantID,
      'SELECT enabled FROM scheduled_tasks WHERE id=$1', [taskID]);
    expect(enabledRows).toEqual([{ enabled: true }]);
    completed.push('scheduled-task.mutation.enable');
    recordEvidence(evidence, 'scheduled task enable toggle');

    // 删除：Modal.confirm 确认后 DELETE，DB 断言行消失。
    const deleteResponse = waitForMutation(page, `/scheduled-tasks/${taskID}`, 'DELETE');
    await page.getByRole('button', { name: `删除定时任务 ${renamed}` }).click();
    await page.locator('.ant-modal-confirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteResponse).status()).toBe(200);
    const deletedRows = await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM scheduled_tasks WHERE id=$1', [taskID]);
    expect(deletedRows).toEqual([{ count: '0' }]);
    completed.push('scheduled-task.mutation.delete');
    recordEvidence(evidence, 'scheduled task deletion');
  } catch (err) {
    primaryError = err;
  } finally {
    const cleanupFailures: unknown[] = [];
    for (const task of cleanupTasks) {
      try {
        await task();
      } catch (err) {
        cleanupFailures.push(err);
      }
    }
    if (primaryError) throw acceptanceErrors([primaryError, ...cleanupFailures]);
    const failure = acceptanceErrors(cleanupFailures);
    if (failure) throw failure;
  }
  return completed;
};
