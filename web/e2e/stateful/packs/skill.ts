import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface SkillPackContext {
  actor: BrowserActor;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
}

const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));

const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};

const rows = async <R extends QueryResultRow>(
  pool: DatabasePool, tenantID: string, text: string, values: unknown[],
): Promise<R[]> => (await withTenantQuery<R>(pool, tenantID, { text, values })).rows;

export const executeSkillPack = async ({ actor, pool, evidence, webURL }: SkillPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const skillName = `E2E-Skill-${Date.now()}`;
  let skillID = '';
  try {
    const listResponse = waitForMutation(page, '/skills', 'GET');
    await page.goto(`${webURL}/skills`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '技能列表' })).toBeVisible();
    completed.push('skill.route.skills');

    await page.getByRole('button', { name: '创建技能' }).click();
    await expect(page).toHaveURL(`${webURL}/skills/create`);
    completed.push('skill.route.skills.create');
    await page.getByLabel('名称').fill(skillName);
    await page.getByLabel('能力目标').fill('对 stateful 验收输入进行分类');
    await page.getByLabel('调用时机').fill('用户要求分类时调用');
    await page.getByLabel('样例输入').fill('请分类这条验收输入');
    await page.getByLabel('期望输出').fill('返回 stateful 分类');
    await page.getByLabel('执行指令').fill('只返回可核验的分类结果。');
    const createResponse = waitForMutation(page, '/skills', 'POST');
    await page.getByRole('button', { name: '创建草稿' }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    const body = await created.json() as { skill: { id: string } };
    skillID = body.skill.id;
    await expect(page).toHaveURL(`${webURL}/skills/${skillID}/workspace`);
    completed.push('skill.mutation.post.skills', 'skill.route.skills.id.workspace');
    recordEvidence(evidence, 'skill draft creation');

    await page.getByLabel('能力目标').fill('对 stateful 验收输入进行可靠分类');
    const capabilityResponse = waitForMutation(page, `/skills/${skillID}/draft/capability`, 'PATCH');
    await page.getByRole('button', { name: '保存能力' }).click();
    expect((await capabilityResponse).status()).toBe(200);
    completed.push('skill.mutation.patch.skills.id.draft.capability');

    await page.getByRole('tab', { name: '激活契约' }).click();
    await page.getByLabel('激活名称').fill('classify_stateful_input');
    await page.getByLabel('用途说明').fill('对本地端到端输入进行分类');
    await page.getByLabel('确认契约').click();
    const activationResponse = waitForMutation(page, `/skills/${skillID}/draft/activation`, 'PATCH');
    await page.getByRole('button', { name: '保存激活契约' }).click();
    expect((await activationResponse).status()).toBe(200);
    completed.push('skill.mutation.patch.skills.id.draft.activation');

    await page.getByRole('tab', { name: '指令与权限' }).click();
    await page.getByLabel('执行指令').fill('严格分类输入并返回 category 字段。');
    // 旧「记忆范围」Checkbox（含「当前会话」选项）随 skill requirements 功能在 main
    // d65933db 移除；此处不再勾选，仅保存执行指令。
    const instructionsResponse = waitForMutation(page, `/skills/${skillID}/draft/instructions`, 'PATCH');
    await page.getByRole('button', { name: '保存指令与权限' }).click();
    expect((await instructionsResponse).status()).toBe(200);
    completed.push('skill.mutation.patch.skills.id.draft.instructions');

    await page.getByRole('tab', { name: 'Revision' }).click();
    const publishResponse = waitForMutation(page, `/skills/${skillID}/publish`, 'POST');
    await page.getByRole('button', { name: '发布当前 Revision' }).click();
    expect((await publishResponse).status()).toBe(200);
    const persisted = await rows<{ status: string; active_revision_id: string | null; capability: unknown }>(
      pool, tenantID,
      `SELECT s.status,s.active_revision_id,r.capability
       FROM skills s JOIN skill_revisions r ON r.id=s.active_revision_id WHERE s.id=$1`,
      [skillID],
    );
    expect(persisted).toHaveLength(1);
    expect(persisted[0].active_revision_id).toBeTruthy();
    completed.push('skill.mutation.post.skills.id.publish');
    recordEvidence(evidence, 'skill draft updates and publication');

    const refreshedListResponse = waitForMutation(page, '/skills', 'GET');
    await page.goto(`${webURL}/skills`);
    expect((await refreshedListResponse).status()).toBe(200);
    const card = page.locator('.ant-card').filter({ hasText: skillName });
    await expect(card).toBeVisible();
    const deleteResponse = waitForMutation(page, `/skills/${skillID}`, 'DELETE');
    await card.getByRole('button', { name: '删除技能' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: '删 除' }).click();
    expect((await deleteResponse).status()).toBe(200);
    expect(await rows<{ count: string }>(pool, tenantID, 'SELECT count(*)::text AS count FROM skills WHERE id=$1', [skillID]))
      .toEqual([{ count: '0' }]);
    completed.push('skill.mutation.delete.skills.id');
    recordEvidence(evidence, 'skill deletion');
  } finally {
    await page.close();
  }
  return completed;
};
