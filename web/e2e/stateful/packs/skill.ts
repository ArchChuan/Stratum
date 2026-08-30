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

    // 草稿-发布双阶段（feat/knowledge-versioning-skill-draft）：保存草稿不生效，
    // 发布后 active_revision_id 才指向新版本，旧版降级 deprecated。
    await expect(page.getByLabel('执行指令')).toBeVisible();
    const activeBeforeDraft = await rows<{ active_revision_id: string }>(
      pool, tenantID, 'SELECT active_revision_id FROM skills WHERE id=$1', [skillID],
    );
    const activeBefore = activeBeforeDraft[0].active_revision_id;
    expect(activeBefore).toBeTruthy();

    // 保存草稿：POST /skills/:id/draft → 200，active 指针不变。
    await page.getByLabel('执行指令').fill('严格分类输入并返回 category 字段，输出可核验。');
    const draftResponse = waitForMutation(page, `/skills/${skillID}/draft`, 'POST');
    await page.getByRole('button', { name: '保存草稿' }).click();
    expect((await draftResponse).status()).toBe(200);
    await expect(page.getByText('有未发布的草稿')).toBeVisible({ timeout: 10000 });
    completed.push('skill.mutation.post.skills.id.draft');
    recordEvidence(evidence, 'skill draft save');

    // 持久化（草稿态）：draft_revision_id 指向 status=draft 且 revision_no 为 NULL 的行，
    // active_revision_id 保持原样（草稿未生效）。
    const draftState = await rows<{ active_revision_id: string | null; draft_revision_id: string | null }>(
      pool, tenantID,
      `SELECT active_revision_id, draft_revision_id FROM skills WHERE id=$1`,
      [skillID],
    );
    expect(draftState).toHaveLength(1);
    expect(draftState[0].active_revision_id).toBe(activeBefore);
    expect(draftState[0].draft_revision_id).toBeTruthy();
    const draftRev = await rows<{ status: string; revision_no: number | null }>(
      pool, tenantID,
      `SELECT status, revision_no FROM skill_revisions WHERE id=$1`,
      [draftState[0].draft_revision_id],
    );
    expect(draftRev).toHaveLength(1);
    expect(draftRev[0].status).toBe('draft');
    expect(draftRev[0].revision_no).toBeNull();
    completed.push('skill.db.draft.persisted');
    recordEvidence(evidence, 'skill draft persisted');

    // 发布：POST /skills/:id/publish → 200，active_revision_id 指向新 revision(revision_no=2)，
    // draft_revision_id 清空，旧版降级 deprecated。
    const publishResponse = waitForMutation(page, `/skills/${skillID}/publish`, 'POST');
    await page.getByRole('button', { name: /^发\s*布$/ }).click();
    expect((await publishResponse).status()).toBe(200);
    await expect(page.getByText('有未发布的草稿')).toHaveCount(0, { timeout: 10000 });
    completed.push('skill.mutation.post.skills.id.publish');
    recordEvidence(evidence, 'skill publish');

    const published = await rows<{ active_revision_id: string | null; draft_revision_id: string | null; status: string }>(
      pool, tenantID,
      `SELECT s.active_revision_id, s.draft_revision_id, s.status
       FROM skills s WHERE s.id=$1`,
      [skillID],
    );
    expect(published[0].active_revision_id).not.toBe(activeBefore);
    expect(published[0].draft_revision_id).toBeNull();
    const revs = await rows<{ revision_no: number | null; status: string }>(
      pool, tenantID,
      `SELECT revision_no, status FROM skill_revisions WHERE skill_id=$1 ORDER BY revision_no NULLS LAST`,
      [skillID],
    );
    expect(revs).toEqual([
      { revision_no: 1, status: 'deprecated' },
      { revision_no: 2, status: 'published' },
    ]);
    completed.push('skill.db.revision.rotated');
    recordEvidence(evidence, 'skill revision rotated');

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
