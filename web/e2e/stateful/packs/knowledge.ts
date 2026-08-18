import { expect } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { configureManagedModels, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { runCleanupTasks } from '../core/errors';

interface KnowledgePackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string; fixtureURL: string; backendURL: string }
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const waitFor = (page: import('@playwright/test').Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));

export const executeKnowledgePack = async ({ actor, pool, evidence, webURL, fixtureURL, backendURL }: KnowledgePackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  await configureManagedModels(pool, tenantID, fixtureURL, actor.accessToken ?? '', backendURL);
  const page = await actor.context.newPage();
  const workspace = `e2e-kb-${Date.now()}`;
  let documentID = '';
  try {
    const listResponse = waitFor(page, '/knowledge/workspaces', 'GET');
    await page.goto(`${webURL}/knowledge`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: '知识库' })).toBeVisible();

    await page.getByRole('button', { name: '新建知识库' }).click();
    const dialog = page.getByRole('dialog', { name: '新建知识库' });
    await dialog.getByLabel('名称').fill(workspace);
    await dialog.getByLabel('描述').fill('Stateful E2E 产品文档与验收知识');
    await dialog.getByLabel('嵌入模型').click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option')
      .filter({ hasText: /^text-embedding-v3$/ }).first().click();
    const createResponse = waitFor(page, '/knowledge/workspaces', 'POST');
    await dialog.getByRole('button', { name: /创\s*建/ }).click();
    expect((await createResponse).status()).toBe(201);
    expect(await rows<{ description: string }>(pool, tenantID,
      'SELECT description FROM rag_workspaces WHERE name=$1', [workspace]))
      .toEqual([{ description: 'Stateful E2E 产品文档与验收知识' }]);

    await page.locator('.ant-card').filter({ hasText: workspace }).getByRole('button', { name: '查看知识库' }).click();
    await expect(page).toHaveURL(`${webURL}/knowledge/${workspace}`);
    await expect(page.getByRole('heading', { name: workspace })).toBeVisible();

    const topKInput = page.getByLabel('Top-K', { exact: true });
    await expect(topKInput).toHaveValue('5');
    await topKInput.fill('6');
    await expect(topKInput).toHaveValue('6');
    const updateResponse = waitFor(page, `/knowledge/workspaces/${workspace}`, 'PATCH');
    await page.getByRole('button', { name: /保\s*存/ }).click();
    expect((await updateResponse).status()).toBe(200);
    expect(await rows<{ top_k: number }>(pool, tenantID,
      "SELECT (config->>'top_k')::int AS top_k FROM rag_workspaces WHERE name=$1", [workspace]))
      .toEqual([{ top_k: 6 }]);

    // ── P1 拒答场景 1：空库查询 → no_sources + best_score=0 ──────────────
    // 上传文档前 query：响应带 no_answer.reason=no_sources（retrieved_count
    // 为 0、best_score 恒填 0）；UI 拒答 Alert 替换绿色回答卡。
    const emptyQueryResponse = waitFor(page, '/knowledge/query', 'POST');
    await page.getByPlaceholder('输入查询问题...').fill('完全不存在的内容 e2e-empty');
    await page.getByRole('button', { name: /查\s*询/ }).click();
    const emptyQueryBody = await (await emptyQueryResponse).json();
    expect(emptyQueryBody.no_answer.reason).toBe('no_sources');
    expect(emptyQueryBody.no_answer.retrieved_count).toBe(0);
    expect(emptyQueryBody.best_score).toBe(0);
    expect(emptyQueryBody.candidate_count).toBe(0);
    // Alert message 与 detail 都含「知识库中未检索到相关内容」，正则会命中
    // 两个节点（strict mode violation），分别精确匹配 message 全文与 detail。
    await expect(page.getByText('知识库中未检索到相关内容，未基于知识库作答。')).toBeVisible();
    await expect(page.getByText('知识库中未检索到相关内容', { exact: true })).toBeVisible();
    await expect(page.getByText('回答', { exact: true })).toHaveCount(0);

    const ingestResponse = waitFor(page, '/knowledge/ingest', 'POST');
    await page.locator('input[type="file"]').setInputFiles({
      name: 'stateful-knowledge.txt', mimeType: 'text/plain',
      buffer: Buffer.from('Stratum stateful knowledge acceptance proves real browser database and vector retrieval.'),
    });
    expect((await ingestResponse).status()).toBe(202);
    await expect.poll(async () => {
      const docs = await rows<{ id: string; ingest_status: string; ingest_error: string }>(pool, tenantID,
        `SELECT kd.id::text,kd.ingest_status,kd.ingest_error FROM knowledge_docs kd
         JOIN rag_workspaces rw ON rw.id=kd.workspace_id WHERE rw.name=$1 ORDER BY kd.created_at DESC LIMIT 1`, [workspace]);
      documentID = docs[0]?.id ?? '';
      if (docs[0]?.ingest_status === 'failed') throw new Error(`knowledge ingest failed: ${docs[0].ingest_error}`);
      return docs[0]?.ingest_status;
    }, { timeout: 120_000 }).toBe('completed');
    await page.reload();
    await expect(page.getByText('stateful-knowledge.txt', { exact: true })).toBeVisible({ timeout: 15_000 });

    // ── P1 拒答场景 2：阈值全滤 → threshold_filtered + best_score 透传 ────
    // score_threshold=0.99 全滤：响应带 no_answer.reason=threshold_filtered
    // + retrieved_count>0 + best_score>0（恒填充，不随过滤归零）；
    // UI 拒答 Alert 显示过滤统计，绿色回答卡不渲染。
    const thresholdInput = page.getByLabel('分数阈值');
    await thresholdInput.fill('0.99');
    const thresholdUpdate = waitFor(page, `/knowledge/workspaces/${workspace}`, 'PATCH');
    await page.getByRole('button', { name: /保\s*存/ }).click();
    expect((await thresholdUpdate).status()).toBe(200);
    const thresholdRows = await rows<{ score_threshold: number }>(pool, tenantID,
      "SELECT (config->>'score_threshold')::float AS score_threshold FROM rag_workspaces WHERE name=$1", [workspace]);
    expect(thresholdRows[0]?.score_threshold).toBeCloseTo(0.99, 2);

    const filteredQueryResponse = waitFor(page, '/knowledge/query', 'POST');
    await page.getByPlaceholder('输入查询问题...').fill('stateful acceptance 验证什么？');
    await page.getByRole('button', { name: /查\s*询/ }).click();
    const filteredBody = await (await filteredQueryResponse).json();
    expect(filteredBody.no_answer.reason).toBe('threshold_filtered');
    expect(filteredBody.no_answer.retrieved_count).toBeGreaterThan(0);
    expect(filteredBody.no_answer.filtered_count).toBeGreaterThan(0);
    expect(filteredBody.best_score).toBeGreaterThan(0);
    // 与场景 1 同理：精确匹配 message 全文；统计句断言 detail 节点。
    await expect(page.getByText('检索到的候选均未达到相关性阈值，未基于知识库作答。')).toBeVisible();
    await expect(page.getByText(/阈值过滤 \d+ 条/)).toBeVisible();
    await expect(page.getByText('回答', { exact: true })).toHaveCount(0);

    // ── P1 拒答场景 3：恢复默认 → 有答案、无 no_answer 键（omitempty）────
    // score_threshold 回写 0：响应无 no_answer 键、best_score>0、sources
    // 非空；UI 绿色回答卡恢复（响应逐字节兼容旧后端，仅新增字段）。
    await thresholdInput.fill('0');
    const restoreUpdate = waitFor(page, `/knowledge/workspaces/${workspace}`, 'PATCH');
    await page.getByRole('button', { name: /保\s*存/ }).click();
    expect((await restoreUpdate).status()).toBe(200);

    const queryResponse = waitFor(page, '/knowledge/query', 'POST');
    await page.getByPlaceholder('输入查询问题...').fill('stateful acceptance 验证什么？');
    await page.getByRole('button', { name: /查\s*询/ }).click();
    expect((await queryResponse).status()).toBe(200);
    const queryBody = await (await queryResponse).json();
    expect(queryBody.no_answer).toBeUndefined();
    expect(queryBody.best_score).toBeGreaterThan(0);
    expect(queryBody.sources.length).toBeGreaterThan(0);
    await expect(page.getByText('回答', { exact: true })).toBeVisible();
    await expect(page.getByText(/stateful/i).last()).toBeVisible();

    const deleteDocumentButton = page.getByRole('button', { name: '删除文档' });
    await expect(deleteDocumentButton).toBeEnabled({ timeout: 15_000 });
    const deleteDocumentResponse = waitFor(page,
      `/knowledge/workspaces/${workspace}/documents/${documentID}`, 'DELETE');
    await deleteDocumentButton.click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteDocumentResponse).status()).toBe(200);
    expect(await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM knowledge_docs WHERE id=$1::uuid', [documentID]))
      .toEqual([{ count: '0' }]);

    await page.goto(`${webURL}/knowledge`);
    const card = page.locator('.ant-card').filter({ hasText: workspace });
    const deleteResponse = waitFor(page, `/knowledge/workspaces/${workspace}`, 'DELETE');
    await card.getByRole('button', { name: '删除知识库' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteResponse).status()).toBe(200);
    expect(await rows<{ count: string }>(pool, tenantID,
      'SELECT count(*)::text AS count FROM rag_workspaces WHERE name=$1', [workspace]))
      .toEqual([{ count: '0' }]);

    evidence.ui.push('Knowledge CRUD, ingest, query, no-answer refusal alerts, and document cleanup completed through Chromium controls');
    evidence.http.push('Knowledge route, mutation, and query refusal responses succeeded');
    evidence.database.push('Knowledge workspace, document, and config state reconciled');
  } finally {
    await runCleanupTasks([
      async () => {
        const existing = await rows<{ count: string }>(pool, tenantID,
          'SELECT count(*)::text AS count FROM rag_workspaces WHERE name=$1', [workspace]);
        if (existing[0]?.count !== '1') return;
        await page.goto(`${webURL}/knowledge`);
        const card = page.locator('.ant-card').filter({ hasText: workspace });
        await card.getByRole('button', { name: '删除知识库' }).click();
        const deleteResponse = waitFor(page, `/knowledge/workspaces/${workspace}`, 'DELETE');
        await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
        expect((await deleteResponse).status()).toBe(200);
      },
      async () => page.close(),
    ]);
  }
  return [
    'knowledge.route.knowledge', 'knowledge.route.knowledge.name',
    'knowledge.mutation.post.knowledge.workspaces', 'knowledge.mutation.patch.knowledge.workspaces.name',
    'knowledge.mutation.post.knowledge.ingest', 'knowledge.mutation.post.knowledge.query',
    'knowledge.mutation.delete.knowledge.workspaces.name.documents.documentid',
    'knowledge.mutation.delete.knowledge.workspaces.name',
  ];
};
