import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import {
  configureManagedModels, requireUUID, withTenantMutation, withTenantQuery, type DatabasePool,
} from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { runCleanupTasks } from '../core/errors';

interface EvaluationPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }
const waitFor = async (page: Page, path: string | RegExp, method: string) => {
  try {
    return await page.waitForResponse((response) => {
      const pathname = new URL(response.url()).pathname;
      return (typeof path === 'string' ? pathname === path : path.test(pathname))
        && response.request().method() === method;
    });
  } catch (error) {
    throw new Error(`waiting for ${method} ${String(path)}: ${error instanceof Error ? error.message : String(error)}`);
  }
};
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const mutate = async (pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantMutation(pool, tenantID, { text, values })
);
const openEvolution = async (page: Page) => {
  await page.getByRole('tab', { name: /候选版本/ }).click();
  await page.getByRole('button', { name: '进化操作' }).click();
  return page.getByRole('dialog', { name: '进化操作' });
};
const closeDrawerIfOpen = async (page: Page) => {
  const drawer = page.locator('.ant-drawer:visible');
  if (await drawer.count()) {
    await page.locator('.ant-drawer-mask:visible').click({ position: { x: 5, y: 5 } });
    await expect(drawer).toBeHidden();
  }
};

const seedDecisionFixtures = async (
  pool: DatabasePool, tenantID: string, suiteRevisionID: string, suffix: string,
) => {
  const policy = JSON.stringify({ stages: [5, 20, 50, 100], min_samples: 100, min_observation_minutes: 60,
    max_cost_regression: 0.15, max_latency_regression: 0.2, max_error_rate_increase: 0.01 });
  const evidence = JSON.stringify({ metrics: { samples: 100, observed_minutes: 60, quality_improvement: 0.2,
    quality_significant: true, cost_regression: 0, p95_latency_regression: 0, error_rate_increase: 0,
    security_violation: false } });
  const fixtures = [
    { action: 'promote', resource: `e2e-promote-${suffix}`, experiment: `e2e-promote-experiment-${suffix}`,
      recommendation: 'promote', snapshot: evidence },
    { action: 'rollback', resource: `e2e-rollback-${suffix}`, experiment: `e2e-rollback-experiment-${suffix}`,
      recommendation: 'hold', snapshot: '{}' },
  ];
  for (const fixture of fixtures) {
    const stable = `${fixture.resource}-stable`;
    const canary = `${fixture.resource}-canary`;
    const optimizationJob = `${fixture.resource}-optimization`;
    const candidate = `${fixture.resource}-candidate`;
    await mutate(pool, tenantID, `
      INSERT INTO resource_revisions
        (id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary,published_at)
      VALUES ($1,'mcp',$2,'manual','published',$3,$3,$4,$5::jsonb,now()),
             ($6,'mcp',$2,'optimization','draft',$7,$7,$8,$9::jsonb,NULL)`,
    [stable, fixture.resource, `hash-${stable}`, `fixture://${stable}`,
      JSON.stringify({ name: fixture.resource, revision: 'stable' }), canary, `hash-${canary}`, `fixture://${canary}`,
      JSON.stringify({ name: fixture.resource, revision: 'canary' })]);
    await mutate(pool, tenantID, `
      INSERT INTO optimization_jobs
        (id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status,completed_at)
      VALUES ($1,'mcp',$2,$3,$4,'succeeded',now())`,
    [optimizationJob, fixture.resource, stable, suiteRevisionID]);
    await mutate(pool, tenantID, `
      INSERT INTO optimization_candidates
        (id,optimization_job_id,revision_id,parent_revision_id,source,status)
      VALUES ($1,$2,$3,$4,'optimization','proposed')`,
    [candidate, optimizationJob, canary, stable]);
    await mutate(pool, tenantID, `
      INSERT INTO evaluation_experiments
        (id,resource_kind,resource_id,stable_revision_id,canary_revision_id,suite_revision_id,status,stage_percent,
         policy,decision_snapshot,state_version,recommendation,safety_stopped)
      VALUES ($1,'mcp',$2,$3,$4,$5,'running',5,$6::jsonb,$7::jsonb,1,$8,false)`,
    [fixture.experiment, fixture.resource, stable, canary, suiteRevisionID, policy, fixture.snapshot,
      fixture.recommendation]);
    await mutate(pool, tenantID, `
      INSERT INTO evaluation_deployments
        (resource_kind,resource_id,stable_revision_id,canary_revision_id,canary_percent,experiment_id)
      VALUES ('mcp',$1,$2,$3,5,$4)`, [fixture.resource, stable, canary, fixture.experiment]);
  }
  return fixtures.map((fixture) => ({ ...fixture, stable: `${fixture.resource}-stable`, canary: `${fixture.resource}-canary` }));
};

export const executeEvaluationPack = async ({
  actor, pool, evidence, webURL,
}: EvaluationPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const userID = requireUUID(actor.userID ?? '', 'user_id');
  await configureManagedModels(pool, tenantID);
  const page = await actor.context.newPage();
  const suffix = String(Date.now());
  const skillName = `E2E-Evaluation-Skill-${suffix}`;
  const agentName = `E2E-Evaluation-Agent-${suffix}`;
  let skillID = '';
  let agentID = '';
  let suiteID = '';
  let suiteRevisionID = '';
  let runID = '';
  let experimentID = '';
  let fixtureIDs: string[] = [];
  try {
    await page.goto(`${webURL}/skills/create`);
    await page.getByLabel('名称').fill(skillName);
    await page.getByLabel('能力目标').fill('返回可核验的 stateful 评测结果');
    await page.getByLabel('调用时机').fill('执行 stateful 评测时');
    await page.getByLabel('样例输入').fill('执行 stateful evaluation');
    await page.getByLabel('期望输出').fill('stateful sync completed');
    await page.getByLabel('执行指令').fill('返回 stateful sync completed。');
    const skillResponse = waitFor(page, '/skills', 'POST');
    await page.getByRole('button', { name: '创建草稿' }).click();
    const createdSkill = await skillResponse;
    expect(createdSkill.status()).toBe(201);
    skillID = (await createdSkill.json() as { skill: { id: string } }).skill.id;
    await page.getByRole('tab', { name: '激活契约' }).click();
    await page.getByLabel('激活名称').fill('evaluate_stateful_input');
    await page.getByLabel('用途说明').fill('执行本地 stateful 评测');
    await page.getByLabel('确认契约').click();
    const activationResponse = waitFor(page, `/skills/${skillID}/draft/activation`, 'PATCH');
    await page.getByRole('button', { name: '保存激活契约' }).click();
    expect((await activationResponse).status()).toBe(200);
    await page.getByRole('tab', { name: 'Revision' }).click();
    const publishResponse = waitFor(page, `/skills/${skillID}/publish`, 'POST');
    await page.getByRole('button', { name: '发布当前 Revision' }).click();
    const published = await publishResponse;
    expect(published.status()).toBe(200);
    const stableRevisionID = (await published.json() as { id: string }).id;

    const agentSkillsResponse = waitFor(page, '/skills', 'GET');
    await page.goto(`${webURL}/agents/create`);
    const listedSkills = await agentSkillsResponse;
    expect(listedSkills.status()).toBe(200);
    expect((await listedSkills.json() as { skills: Array<{ id: string; name: string }> }).skills)
      .toEqual(expect.arrayContaining([expect.objectContaining({ id: skillID, name: skillName })]));
    await page.getByLabel('名称').fill(agentName);
    await page.getByLabel('系统提示词').fill('执行激活的 Skill，并返回确定的 stateful 结果。');
    const modelInput = page.getByRole('combobox', { name: 'LLM 模型' });
    await modelInput.fill('qwen-max');
    await modelInput.press('Enter');
    const agentResponse = waitFor(page, '/agents', 'POST');
    await page.getByRole('button', { name: '创建 Agent' }).click();
    const createdAgent = await agentResponse;
    expect(createdAgent.status()).toBe(201);
    agentID = (await createdAgent.json() as { id: string }).id;
    await mutate(pool, tenantID,
      'INSERT INTO agent_skill_links(agent_id,skill_id,revision_id) VALUES ($1,$2,$3)',
      [agentID, skillID, stableRevisionID]);
    expect(await rows<{ skill_id: string }>(pool, tenantID,
      'SELECT skill_id FROM agent_skill_links WHERE agent_id=$1', [agentID])).toEqual([{ skill_id: skillID }]);

    await mutate(pool, tenantID, `
      INSERT INTO resource_revisions
        (id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary,published_at)
      VALUES ($1,'skill',$2,'manual','published',$3,$3,$4,$5::jsonb,now())
      ON CONFLICT (id) DO NOTHING`,
    [stableRevisionID, skillID, `hash-${stableRevisionID}`, `skill://${stableRevisionID}`,
      JSON.stringify({ name: skillName })]);
    await mutate(pool, tenantID, `
      INSERT INTO evaluation_deployments(resource_kind,resource_id,stable_revision_id,canary_percent)
      VALUES ('skill',$2,$1,0)
      ON CONFLICT (resource_kind,resource_id) DO UPDATE SET stable_revision_id=EXCLUDED.stable_revision_id`,
    [stableRevisionID, skillID]);

    const routeResponse = waitFor(page, '/evaluations/overview', 'GET');
    await page.goto(`${webURL}/evaluations`);
    expect((await routeResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '评测与进化中心' })).toBeVisible();
    await page.getByRole('button', { name: /新建评测/ }).click();
    const createDialog = page.getByRole('dialog', { name: '新建评测' });
    await createDialog.getByRole('combobox', { name: '目标资源' }).click();
    await page.locator('.ant-select-item-option-content').filter({ hasText: skillName }).click();
    await createDialog.getByLabel('评测名称').fill(`E2E Stateful Suite ${suffix}`);
    await createDialog.getByLabel('评测说明').fill('真实浏览器发起的 stateful evaluation');
    await createDialog.getByLabel('用例名称').fill('确定性 Skill 输出');
    await createDialog.getByLabel('测试输入').fill('执行 stateful evaluation');
    await createDialog.getByLabel('期望输出').fill('stateful sync completed');
    const suiteResponse = waitFor(page, '/evaluations/suites', 'POST');
    const publishSuiteResponse = waitFor(page, /\/evaluations\/suites\/[^/]+\/publish$/, 'POST');
    const runResponse = waitFor(page, '/evaluations/runs', 'POST');
    await createDialog.getByRole('button', { name: '创建并运行' }).click();
    const suiteCreated = await suiteResponse;
    expect(suiteCreated.status()).toBe(201);
    suiteID = (await suiteCreated.json() as { suite: { id: string } }).suite.id;
    const suitePublished = await publishSuiteResponse;
    expect(suitePublished.status()).toBe(200);
    suiteRevisionID = (await suitePublished.json() as { id: string }).id;
    expect((await runResponse).status()).toBe(202);
    await expect.poll(async () => (await rows<{ id: string; status: string }>(pool, tenantID,
      `SELECT id,status FROM eval_runs WHERE resource_id=$1 AND suite_revision_id=$2 ORDER BY created_at DESC LIMIT 1`,
    [skillID, suiteRevisionID]))[0]?.status, { timeout: 120_000 }).toBe('succeeded');
    const run = (await rows<{ id: string; trace_id: string }>(pool, tenantID, `
      SELECT r.id,cr.trace_id FROM eval_runs r JOIN eval_case_results cr ON cr.run_id=r.id
      WHERE r.resource_id=$1 AND r.suite_revision_id=$2 ORDER BY r.created_at DESC LIMIT 1`,
    [skillID, suiteRevisionID]))[0];
    runID = run.id;
    expect(run.trace_id).not.toBe('');

    let dialog = await openEvolution(page);
    let panel = dialog.locator('.ant-tabs-tabpane-active');
    await panel.getByRole('combobox', { name: '资源类型' }).click();
    await page.locator('.ant-select-item-option-content').filter({ hasText: 'Skill' }).click();
    await panel.getByLabel('资源 ID').fill(skillID);
    await panel.getByLabel('稳定 Revision ID').fill(stableRevisionID);
    await panel.getByLabel('Suite Revision ID').fill(suiteRevisionID);
    await panel.getByLabel('失败摘要').fill('输出需要更明确且可核验');
    const optimizationResponse = waitFor(page, '/evaluations/optimizations', 'POST');
    await dialog.getByRole('button', { name: '生成候选' }).click();
    const optimized = await optimizationResponse;
    expect(optimized.status()).toBe(201);
    const candidates = (await optimized.json() as { candidates: Array<{ id: string; revision: { revision_id: string } }> }).candidates;
    expect(candidates).toHaveLength(2);
    await expect(dialog).toBeHidden();

    await page.getByRole('tab', { name: /候选版本/ }).click();
    const evaluatedRow = page.getByRole('row').filter({ hasText: candidates[1].id });
    await evaluatedRow.getByRole('button', { name: '详情' }).click();
    const candidateDrawer = page.locator('.ant-drawer:visible');
    await candidateDrawer.getByRole('button', { name: '运行离线评测' }).click();
    const evaluationDialog = page.getByRole('dialog', { name: '运行候选离线评测' });
    await evaluationDialog.getByLabel('Suite Revision ID').fill(suiteRevisionID);
    const candidateRunResponse = waitFor(page, '/evaluations/runs', 'POST');
    await evaluationDialog.getByRole('button', { name: '开始评测' }).click();
    expect((await candidateRunResponse).status()).toBe(202);
    await expect(evaluationDialog).toBeHidden();
    await expect.poll(async () => (await rows<{ status: string; passed: boolean }>(pool, tenantID, `
      SELECT status,passed FROM eval_runs WHERE resource_id=$1 AND revision_id=$2 AND suite_revision_id=$3
      ORDER BY created_at DESC LIMIT 1`, [skillID, candidates[1].revision.revision_id, suiteRevisionID]))[0],
    { timeout: 120_000 }).toEqual({ status: 'succeeded', passed: true });
    await closeDrawerIfOpen(page);
    await page.reload();
    await page.getByRole('tab', { name: /候选版本/ }).click();

    const rejectedRow = page.getByRole('row').filter({ hasText: candidates[0].id });
    await rejectedRow.getByRole('button', { name: '详情' }).click();
    const rejectResponse = waitFor(page, `/evaluations/candidates/${candidates[0].id}/reject`, 'POST');
    await page.getByRole('button', { name: '拒绝候选' }).click();
    const rejectDialog = page.getByRole('dialog', { name: '确认拒绝此候选版本？' });
    await rejectDialog.getByRole('button', { name: '拒绝候选' }).click();
    expect((await rejectResponse).status()).toBe(200);
    await expect(rejectDialog).toBeHidden();
    await closeDrawerIfOpen(page);

    dialog = await openEvolution(page);
    await dialog.getByRole('tab', { name: '创建金丝雀' }).click();
    panel = dialog.locator('.ant-tabs-tabpane-active');
    await panel.getByRole('combobox', { name: '资源类型' }).click();
    await page.locator('.ant-select-item-option-content').filter({ hasText: 'Skill' }).click();
    await panel.getByLabel('资源 ID').fill(skillID);
    await panel.getByLabel('稳定 Revision ID').fill(stableRevisionID);
    await panel.getByLabel('候选 Revision ID').fill(candidates[1].revision.revision_id);
    await panel.getByLabel('Suite Revision ID').fill(suiteRevisionID);
    const experimentResponse = waitFor(page, '/evaluations/experiments', 'POST');
    await dialog.getByRole('button', { name: '创建金丝雀' }).click();
    const experimentCreated = await experimentResponse;
    if (experimentCreated.status() !== 201) {
      throw new Error(`experiment status ${experimentCreated.status()}: ${await experimentCreated.text()}`);
    }
    experimentID = (await experimentCreated.json() as { experiment: { id: string } }).experiment.id;
    await expect(dialog).toBeHidden();

    const evidenceRegistration = await page.request.post('http://127.0.0.1:19091/e2e/opik/register', { data: {
      trace_id: run.trace_id, tenant_id: tenantID, user_id: userID,
      resource_id: skillID, revision_id: stableRevisionID,
    } });
    expect(evidenceRegistration.status()).toBe(204);
    dialog = await openEvolution(page);
    await dialog.getByRole('tab', { name: '记录反馈' }).click();
    panel = dialog.locator('.ant-tabs-tabpane-active');
    await panel.getByLabel('Trace ID').fill(run.trace_id);
    await panel.getByLabel('反馈资源 ID').fill(skillID);
    await panel.getByLabel('分数').fill('0.9');
    const feedbackResponse = waitFor(page, '/evaluations/feedback', 'POST');
    await dialog.getByRole('button', { name: '提交反馈' }).click();
    const feedback = await feedbackResponse;
    if (feedback.status() !== 201) {
      throw new Error(`feedback status ${feedback.status()}: ${await feedback.text()}`);
    }
    await expect(dialog).toBeHidden();

    await page.getByRole('tab', { name: /金丝雀实验/ }).click();
    await page.getByRole('row').filter({ hasText: experimentID }).getByRole('button', { name: '详情' }).click();
    const experimentDrawer = page.locator('.ant-drawer:visible');
    await expect(experimentDrawer).toBeVisible();
    const pauseResponse = waitFor(page, `/evaluations/experiments/${experimentID}/pause`, 'POST');
    await experimentDrawer.getByRole('button', { name: '暂停实验' }).click();
    const pauseDialog = page.getByRole('dialog', { name: '确认暂停金丝雀实验？' });
    await expect(pauseDialog).toBeVisible();
    await pauseDialog.getByRole('button', { name: /暂\s*停/ }).click();
    expect((await pauseResponse).status()).toBe(200);
    await expect.poll(async () => (await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM evaluation_experiments WHERE id=$1', [experimentID]))[0]?.status,
    { timeout: 15_000 }).toBe('paused');
    await closeDrawerIfOpen(page);

    const fixtures = await seedDecisionFixtures(pool, tenantID, suiteRevisionID, suffix);
    fixtureIDs = fixtures.map(({ experiment }) => experiment);
    await page.reload();
    for (const fixture of fixtures) {
      await page.getByRole('tab', { name: /金丝雀实验/ }).click();
      await page.getByRole('row').filter({ hasText: fixture.experiment }).getByRole('button', { name: '详情' }).click();
      const decisionDrawer = page.locator('.ant-drawer:visible');
      await expect(decisionDrawer).toBeVisible();
      const commandResponse = waitFor(page,
        `/evaluations/experiments/${fixture.experiment}/${fixture.action}`, 'POST');
      const label = fixture.action === 'promote' ? '晋级' : '回滚';
      const actionName = new RegExp(label.split('').join('\\s*'));
      await decisionDrawer.getByRole('button', { name: actionName }).click();
      const decisionDialog = page.getByRole('dialog', { name: `确认${label}此实验？` });
      await decisionDialog.getByRole('button', { name: actionName }).click();
      const commandResult = await commandResponse;
      if (commandResult.status() !== 200) {
        throw new Error(`${fixture.action} status ${commandResult.status()}: ${await commandResult.text()}`);
      }
      await expect(decisionDialog).toBeHidden();
      const expectedStatus = fixture.action === 'promote' ? 'completed' : 'rolled_back';
      await expect.poll(async () => (await rows<{ status: string }>(pool, tenantID,
        'SELECT status FROM evaluation_experiments WHERE id=$1', [fixture.experiment]))[0]?.status,
      { timeout: 15_000 }).toBe(expectedStatus);
      const deployment = (await rows<{ stable_revision_id: string; canary_revision_id: string | null }>(pool, tenantID,
        'SELECT stable_revision_id,canary_revision_id FROM evaluation_deployments WHERE resource_id=$1',
      [fixture.resource]))[0];
      expect(deployment.stable_revision_id).toBe(fixture.action === 'promote' ? fixture.canary : fixture.stable);
      expect(deployment.canary_revision_id).toBeNull();
      await closeDrawerIfOpen(page);
      await page.reload();
    }

    expect(await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM optimization_candidates WHERE id=$1', [candidates[0].id])).toEqual([{ status: 'rejected' }]);
    expect(await rows<{ status: string }>(pool, tenantID,
      'SELECT status FROM evaluation_experiments WHERE id=$1', [experimentID])).toEqual([{ status: 'paused' }]);
    expect(await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM evaluation_feedback WHERE trace_id=$1', [run.trace_id]))
      .toEqual([{ count: '1' }]);
    evidence.ui.push('Evaluation suite, run, optimization, experiment, decisions, and feedback completed through Chromium');
    evidence.http.push('All Evaluation mutations returned successful browser-observed responses');
    evidence.database.push('Evaluation run, candidates, experiments, decisions, and feedback reconciled');
  } finally {
    const cleanupTasks: Array<() => Promise<unknown>> = [];
    if (suiteID) {
      const cleanup = [
        { text: 'DELETE FROM evaluation_feedback WHERE resource_id=$1', values: [skillID] },
        { text: 'DELETE FROM experiment_decisions WHERE experiment_id=$1 OR experiment_id=ANY($2::text[])',
          values: [experimentID || 'none', fixtureIDs] },
        { text: 'DELETE FROM evaluation_deployments WHERE resource_id=$1 OR experiment_id=ANY($2::text[])',
          values: [skillID, fixtureIDs] },
        { text: 'DELETE FROM evaluation_experiments WHERE id=$1 OR id=ANY($2::text[])',
          values: [experimentID || 'none', fixtureIDs] },
        { text: 'DELETE FROM optimization_candidates WHERE optimization_job_id IN (SELECT id FROM optimization_jobs WHERE resource_id=$1)',
          values: [skillID] },
        { text: 'DELETE FROM optimization_jobs WHERE resource_id=$1', values: [skillID] },
        { text: 'DELETE FROM optimization_jobs WHERE resource_id LIKE $1 OR resource_id LIKE $2',
          values: [`e2e-promote-${suffix}`, `e2e-rollback-${suffix}`] },
        { text: "DELETE FROM evaluation_jobs WHERE result_id=$1 OR payload->'resource'->>'resource_id'=$2",
          values: [runID || 'none', skillID] },
        { text: 'DELETE FROM eval_case_results WHERE run_id IN (SELECT id FROM eval_runs WHERE resource_id=$1)',
          values: [skillID] },
        { text: 'DELETE FROM eval_runs WHERE resource_id=$1', values: [skillID] },
        { text: 'DELETE FROM eval_suites WHERE id=$1', values: [suiteID] },
        { text: 'DELETE FROM resource_revisions WHERE resource_id=$1 OR id LIKE $2 OR id LIKE $3',
          values: [skillID, `e2e-promote-%-${suffix}`, `e2e-rollback-%-${suffix}`] },
      ];
      cleanupTasks.push(...cleanup.map((query) => async () => mutate(pool, tenantID, query.text, query.values)));
    }
    cleanupTasks.push(
      async () => {
        if (!agentID) return;
				await page.goto(`${webURL}/agents/list`);
        const card = page.locator('.ant-card').filter({ hasText: agentName });
        if (await card.count()) {
          await card.getByRole('button', { name: '删除 Agent' }).click();
          await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
        }
      },
      async () => {
        if (!skillID) return;
        await page.goto(`${webURL}/skills`);
        const card = page.locator('.ant-card').filter({ hasText: skillName });
        if (await card.count()) {
          await card.getByRole('button', { name: '删除技能' }).click();
          await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
        }
      },
      async () => page.close(),
    );
    await runCleanupTasks(cleanupTasks);
  }
  return [
    'evaluation.route.evaluations',
    'evaluation.mutation.post.evaluations.suites',
    'evaluation.mutation.post.evaluations.suites.suiteid.publish',
    'evaluation.mutation.post.evaluations.runs',
    'evaluation.mutation.post.evaluations.optimizations',
    'evaluation.mutation.post.evaluations.experiments',
    'evaluation.mutation.post.evaluations.feedback',
    'evaluation.mutation.post.evaluations.candidates.candidateid.reject',
    'evaluation.mutation.post.evaluations.experiments.experimentid.pause',
    'evaluation.mutation.post.evaluations.experiments.experimentid.promote',
    'evaluation.mutation.post.evaluations.experiments.experimentid.rollback',
  ];
};
