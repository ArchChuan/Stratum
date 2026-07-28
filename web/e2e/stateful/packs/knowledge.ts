import { expect } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { copyConfiguredLLMCredentials, requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { runCleanupTasks } from '../core/errors';

interface KnowledgePackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const waitFor = (page: import('@playwright/test').Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));

export const executeKnowledgePack = async ({ actor, pool, evidence, webURL }: KnowledgePackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  await copyConfiguredLLMCredentials(pool, tenantID, process.env.JWT_PRIVATE_KEY_PEM ?? '');
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
    const createResponse = waitFor(page, '/knowledge/workspaces', 'POST');
    await dialog.getByRole('button', { name: /创\s*建/ }).click();
    expect((await createResponse).status()).toBe(201);
    expect(await rows<{ description: string }>(pool, tenantID,
      'SELECT description FROM rag_workspaces WHERE name=$1', [workspace]))
      .toEqual([{ description: 'Stateful E2E 产品文档与验收知识' }]);

    await page.locator('.ant-card').filter({ hasText: workspace }).getByRole('button', { name: '查看知识库' }).click();
    await expect(page).toHaveURL(`${webURL}/knowledge/${workspace}`);
    await expect(page.getByRole('heading', { name: workspace })).toBeVisible();

    const topKInput = page.getByLabel('Top-K');
    await expect(topKInput).toHaveValue('5');
    await topKInput.fill('6');
    await expect(topKInput).toHaveValue('6');
    const updateResponse = waitFor(page, `/knowledge/workspaces/${workspace}`, 'PATCH');
    await page.getByRole('button', { name: /保\s*存/ }).click();
    expect((await updateResponse).status()).toBe(200);
    expect(await rows<{ top_k: number }>(pool, tenantID,
      "SELECT (config->>'top_k')::int AS top_k FROM rag_workspaces WHERE name=$1", [workspace]))
      .toEqual([{ top_k: 6 }]);

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

    const queryResponse = waitFor(page, '/knowledge/query', 'POST');
    await page.getByPlaceholder('输入查询问题...').fill('stateful acceptance 验证什么？');
    await page.getByRole('button', { name: /查\s*询/ }).click();
    expect((await queryResponse).status()).toBe(200);
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

    evidence.ui.push('Knowledge CRUD, ingest, query, and document cleanup completed through Chromium controls');
    evidence.http.push('Knowledge route and mutation responses succeeded');
    evidence.database.push('Knowledge workspace and document state reconciled');
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
