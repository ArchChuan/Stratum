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

    // 三字段模型（skill 3d037c86 简化）：创建 = 名称/描述/执行指令，无能力目标/激活契约。
    await page.getByRole('button', { name: '创建技能' }).click();
    await expect(page).toHaveURL(`${webURL}/skills/create`);
    completed.push('skill.route.skills.create');
    await page.getByLabel('名称').fill(skillName);
    await page.getByLabel('描述').fill('对 stateful 验收输入进行分类');
    await page.getByLabel('执行指令').fill('严格分类输入并返回 category 字段。');
    // 2 字按钮 AntD 渲染为「创 建」，用正则避免依赖空格插入逻辑。
    const createResponse = waitForMutation(page, '/skills', 'POST');
    await page.getByRole('button', { name: /创\s*建/ }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    const body = await created.json() as { skill: { id: string } };
    skillID = body.skill.id;
    await expect(page).toHaveURL(`${webURL}/skills/${skillID}/workspace`);
    completed.push('skill.mutation.post.skills', 'skill.route.skills.id.workspace');
    recordEvidence(evidence, 'skill creation');

    // 保存即生效：指令 tab 直接 PATCH /skills/:id，无发布步骤。
    await expect(page.getByLabel('执行指令')).toBeVisible();
    await page.getByLabel('执行指令').fill('严格分类输入并返回 category 字段，输出可核验。');
    const saveResponse = waitForMutation(page, `/skills/${skillID}`, 'PATCH');
    await page.getByRole('button', { name: '保存并立即生效' }).click();
    expect((await saveResponse).status()).toBe(200);
    completed.push('skill.mutation.patch.skills.id');
    recordEvidence(evidence, 'skill revision save');

    // 持久化：active_revision 已指向最新 revision（三字段模型无 capability 列）。
    const persisted = await rows<{ status: string; active_revision_id: string | null; name: string }>(
      pool, tenantID,
      `SELECT s.status,s.active_revision_id,r.name
       FROM skills s JOIN skill_revisions r ON r.id=s.active_revision_id WHERE s.id=$1`,
      [skillID],
    );
    expect(persisted).toHaveLength(1);
    expect(persisted[0].active_revision_id).toBeTruthy();
    completed.push('skill.db.revision.persisted');
    recordEvidence(evidence, 'skill revision persisted');

    const refreshedListResponse = waitForMutation(page, '/skills', 'GET');
    await page.goto(`${webURL}/skills`);
    expect((await refreshedListResponse).status()).toBe(200);
    const card = page.locator('.ant-card').filter({ hasText: skillName });
    await expect(card).toBeVisible();
    const deleteResponse = waitForMutation(page, `/skills/${skillID}`, 'DELETE');
    // 删除按钮 aria-label=删除技能；popconfirm okText=删除（2 字按钮渲染「删 除」）。
    await card.getByRole('button', { name: '删除技能' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
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
