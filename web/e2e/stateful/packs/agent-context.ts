import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import {
  configureManagedModels, requireUUID, withTenantMutation, withTenantQuery, type DatabasePool,
} from '../core/database';
import { E2E_MCP_BASE_URL } from '../core/endpoints';
import type { EvidenceRecord } from '../core/evidence';
import { runCleanupTasks } from '../core/errors';

interface AgentContextPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }
const waitFor = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;

export const executeAgentContextPack = async ({
  actor, pool, evidence, webURL,
}: AgentContextPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const userID = requireUUID(actor.userID ?? '', 'user_id');
  await configureManagedModels(pool, tenantID);
  const page = await actor.context.newPage();
  const suffix = String(Date.now());
  const workspace = `e2e-context-${suffix}`;
  const agentName = `E2E-Context-Agent-${suffix}`;
  const knowledgeMarker = `knowledge-context-${suffix}`;
  const memoryMarker = `memory-context-${suffix}`;
  let workspaceID = '';
  let agentID = '';
  try {
    await page.goto(`${webURL}/knowledge`);
    await page.getByRole('button', { name: '新建知识库' }).click();
    const workspaceDialog = page.getByRole('dialog', { name: '新建知识库' });
    await workspaceDialog.getByLabel('名称').fill(workspace);
    await workspaceDialog.getByLabel('描述').fill('Agent Knowledge 与 Memory 上下文联动验收');
    await workspaceDialog.getByLabel('嵌入模型').click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option')
      .filter({ hasText: /^text-embedding-v3$/ }).first().click();
    const workspaceResponse = waitFor(page, '/knowledge/workspaces', 'POST');
    await workspaceDialog.getByRole('button', { name: /创\s*建/ }).click();
    expect((await workspaceResponse).status()).toBe(201);
    workspaceID = (await rows<{ id: string }>(pool, tenantID,
      'SELECT id::text FROM rag_workspaces WHERE name=$1', [workspace]))[0].id;

    await page.locator('.ant-card').filter({ hasText: workspace }).getByRole('button', { name: '查看知识库' }).click();
    const ingestResponse = waitFor(page, '/knowledge/ingest', 'POST');
    await page.locator('input[type="file"]').setInputFiles({
      name: 'agent-context.txt', mimeType: 'text/plain', buffer: Buffer.from(`Context marker: ${knowledgeMarker}`),
    });
    expect((await ingestResponse).status()).toBe(202);
    await expect.poll(async () => {
      const docs = await rows<{ id: string; ingest_status: string }>(pool, tenantID, `
        SELECT kd.id::text,kd.ingest_status FROM knowledge_docs kd JOIN rag_workspaces rw ON rw.id=kd.workspace_id
        WHERE rw.name=$1 ORDER BY kd.created_at DESC LIMIT 1`, [workspace]);
      return docs[0]?.ingest_status;
    }, { timeout: 120_000 }).toBe('completed');

    await withTenantMutation(pool, tenantID, {
      text: `INSERT INTO memory_facts(user_id,scope,content,importance,category,confidence,source,frecency_score)
             VALUES ($1,'user',$2,0.9,'preference',0.95,'manual_api',1)`,
      values: [userID, memoryMarker],
    });

    await page.goto(`${webURL}/agents/create`);
    await page.getByLabel('名称').fill(agentName);
    await page.getByLabel('系统提示词').fill('结合知识与用户记忆回答，并返回 stateful stream completed。');
    const modelInput = page.getByRole('combobox', { name: 'LLM 模型' });
    await modelInput.fill('qwen-max');
    await modelInput.press('Enter');
    await page.locator('.ant-select-selector').filter({ hasText: '选择知识库' }).click();
    await page.locator('.ant-select-item-option-content').filter({ hasText: workspace }).click();
    const agentResponse = waitFor(page, '/agents', 'POST');
    await page.getByRole('button', { name: '创建 Agent' }).click();
    const createdAgent = await agentResponse;
    expect(createdAgent.status()).toBe(201);
    agentID = (await createdAgent.json() as { id: string }).id;
    expect(await rows<{ workspace_id: string }>(pool, tenantID,
      'SELECT workspace_id::text FROM agent_workspaces WHERE agent_id=$1', [agentID]))
      .toEqual([{ workspace_id: workspaceID }]);

    const markerRegistration = await page.request.post(`${E2E_MCP_BASE_URL}/e2e/context/register`, { data: {
      knowledge_marker: knowledgeMarker, memory_marker: memoryMarker,
    } });
    expect(markerRegistration.status()).toBe(204);
    await page.evaluate((id) => sessionStorage.setItem('chat:lastAgentId', id), agentID);
    await page.goto(`${webURL}/chat`);
    await expect(page.getByText(agentName, { exact: true }).first()).toBeVisible();
    await page.getByRole('button', { name: '新建会话' }).click();
    const streamResponse = waitFor(page, `/agents/${agentID}/execute/stream`, 'POST');
    await page.getByPlaceholder(/输入消息/).fill('请结合知识库和我的历史偏好回答');
    await page.getByRole('button', { name: '发送消息' }).click();
    expect((await streamResponse).status()).toBe(200);
    await expect(page.getByText('stateful stream completed', { exact: true }).last()).toBeVisible({ timeout: 120_000 });
    await expect.poll(async () => {
      const response = await page.request.get(`${E2E_MCP_BASE_URL}/e2e/context/evidence`);
      return await response.json() as { knowledge_seen: boolean; memory_seen: boolean };
    }).toEqual({ knowledge_seen: true, memory_seen: true });
    evidence.ui.push('Agent context execution completed through Chromium chat controls');
    evidence.http.push('Agent provider request contained Knowledge and Memory context markers');
    evidence.database.push('Agent workspace binding and user memory row were reconciled');
  } finally {
    await runCleanupTasks([
      async () => {
        if (!agentID) return;
				await page.goto(`${webURL}/agents`);
        const card = page.locator('.ant-card').filter({ hasText: agentName });
        if (await card.count()) {
          await card.getByRole('button', { name: '删除 Agent' }).click();
          await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
        }
      },
      async () => withTenantMutation(pool, tenantID, {
        text: 'DELETE FROM memory_facts WHERE user_id=$1 AND content=$2', values: [userID, memoryMarker],
      }),
      async () => {
        if (!workspaceID) return;
        await page.goto(`${webURL}/knowledge`);
        const card = page.locator('.ant-card').filter({ hasText: workspace });
        if (await card.count()) {
          await card.getByRole('button', { name: '删除知识库' }).click();
          await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
        }
      },
      async () => page.close(),
    ]);
  }
  return [];
};
