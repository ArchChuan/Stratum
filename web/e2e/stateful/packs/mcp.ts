import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import { E2E_MCP_BASE_URL } from '../core/endpoints';
import type { EvidenceRecord } from '../core/evidence';

interface MCPPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path && response.request().method() === method
));
const rows = async <R extends QueryResultRow>(pool: DatabasePool, tenantID: string, text: string, values: unknown[]) => (
  await withTenantQuery<R>(pool, tenantID, { text, values })
).rows;
const recordEvidence = (evidence: EvidenceRecord, label: string) => {
  evidence.ui.push(`${label} completed through Chromium controls`);
  evidence.http.push(`${label} returned the expected HTTP response`);
  evidence.database.push(`${label} persisted state reconciled`);
};
const openSelect = (page: Page, label: string) => page.locator('.ant-form-item').filter({ hasText: label })
  .locator('.ant-select-selector').click();
const findServerRow = async (page: Page, serverName: string) => {
  const row = page.locator('tr').filter({ hasText: serverName });
  const activePage = page.locator('.ant-pagination-item-active');
  if (await activePage.count() > 0 && (await activePage.textContent())?.trim() !== '1') {
    await page.locator('.ant-pagination-item[title="1"]').click();
    await expect(activePage).toHaveText('1');
  }
  while (await row.count() === 0) {
    const next = page.getByRole('button', { name: 'right' });
    if (await next.isDisabled()) break;
    const currentPage = (await activePage.textContent())?.trim() ?? '';
    await next.click();
    await expect(activePage).not.toHaveText(currentPage);
  }
  await expect(row).toBeVisible();
  return row;
};

export const executeMCPPack = async ({ actor, pool, evidence, webURL }: MCPPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const completed: string[] = [];
  const page = await actor.context.newPage();
  const serverName = `E2E-MCP-${Date.now()}`;
  let serverID = '';
  try {
    const listResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await page.goto(`${webURL}/mcp`);
    expect((await listResponse).status()).toBe(200);
    await expect(page.getByRole('heading', { name: 'MCP 服务器' })).toBeVisible();
    completed.push('mcp.route.mcp');

    await page.getByRole('button', { name: '添加服务器' }).click();
    await expect(page).toHaveURL(`${webURL}/mcp/create`);
    completed.push('mcp.route.mcp.create');
    await page.getByLabel('名称').fill(serverName);
    await page.getByLabel('版本').fill('1.0.0');
    await openSelect(page, '传输协议');
    await page.getByLabel('传输协议').press('ArrowDown');
    await page.getByLabel('传输协议').press('Enter');
    await page.getByLabel('服务器 URL').fill(`${E2E_MCP_BASE_URL}/mcp`);
    const createResponse = waitForMutation(page, '/mcp/servers', 'POST');
    const createdListResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await page.getByRole('button', { name: '添加服务器' }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    serverID = (await created.json() as { server_id: string }).server_id;
    await expect(page).toHaveURL(`${webURL}/mcp`);
    const createdList = await createdListResponse;
    expect(createdList.status()).toBe(200);
    expect(JSON.stringify(await createdList.json())).toContain(serverName);
    const row = await findServerRow(page, serverName);
    completed.push('mcp.mutation.post.mcp.servers');
    recordEvidence(evidence, 'MCP connection and discovery');

    await row.getByRole('button', { name: '详情' }).click();
    await expect(page.getByText('stateful_echo', { exact: true })).toBeVisible();
    const policyResponse = waitForMutation(page, `/mcp/tool-policies/${serverID}/stateful_echo`, 'PUT');
    const riskSelect = page.locator('.ant-select:visible').filter({
      has: page.locator('input[aria-label="stateful_echo 风险等级"]'),
    });
    await riskSelect.locator('.ant-select-selector').click();
    await riskSelect.locator('input').press('ArrowDown');
    await riskSelect.locator('input').press('Enter');
    expect((await policyResponse).status()).toBe(200);
    expect(await rows<{ risk_level: string }>(pool, tenantID,
      'SELECT risk_level FROM mcp_tool_policies WHERE server_id=$1 AND tool_name=$2', [serverID, 'stateful_echo']))
      .toEqual([{ risk_level: 'read' }]);
    completed.push('mcp.mutation.put.mcp.tool.policies.serverid.toolname');
    recordEvidence(evidence, 'MCP tool policy update');
    await page.locator('.ant-drawer-close').click();

    await row.getByRole('button', { name: '编辑' }).click();
    await expect(page).toHaveURL(`${webURL}/mcp/${serverID}/edit`);
    completed.push('mcp.route.mcp.id.edit');
    await page.getByLabel('版本').fill('1.0.1');
    const updateResponse = waitForMutation(page, `/mcp/servers/${serverID}`, 'PUT');
    const updatedListResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await page.getByRole('button', { name: '保存并重连' }).click();
    expect((await updateResponse).status()).toBe(200);
    await expect(page).toHaveURL(`${webURL}/mcp`);
    expect((await updatedListResponse).status()).toBe(200);
    expect(await rows<{ version: string }>(pool, tenantID, 'SELECT version FROM mcp_configs WHERE id=$1', [serverID]))
      .toEqual([{ version: '1.0.1' }]);
    completed.push('mcp.mutation.put.mcp.servers.id');

    const updatedRow = await findServerRow(page, serverName);
    const disconnectResponse = waitForMutation(page, `/mcp/servers/${serverID}`, 'DELETE');
    const disconnectedListResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await updatedRow.getByRole('button', { name: '断开' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /断\s*开/ }).click();
    expect((await disconnectResponse).status()).toBe(200);
    expect((await disconnectedListResponse).status()).toBe(200);
    completed.push('mcp.mutation.delete.mcp.servers.id');

    const disconnectedRow = await findServerRow(page, serverName);
    await expect(disconnectedRow.getByRole('button', { name: '连接' })).toBeVisible();
    const reconnectResponse = waitForMutation(page, `/mcp/servers/${serverID}/reconnect`, 'POST');
    const reconnectedListResponse = waitForMutation(page, '/mcp/servers', 'GET');
    await disconnectedRow.getByRole('button', { name: '连接' }).click();
    expect((await reconnectResponse).status()).toBe(200);
    expect((await reconnectedListResponse).status()).toBe(200);
    const reconnectedRow = await findServerRow(page, serverName);
    await expect(reconnectedRow.getByRole('button', { name: '断开' })).toBeVisible();
    completed.push('mcp.mutation.post.mcp.servers.id.reconnect');
    recordEvidence(evidence, 'MCP edit disconnect and reconnect');

    const deleteResponse = waitForMutation(page, `/mcp/servers/${serverID}/config`, 'DELETE');
    await reconnectedRow.getByRole('button', { name: '删除' }).click();
    await page.locator('.ant-popconfirm').getByRole('button', { name: /删\s*除/ }).click();
    expect((await deleteResponse).status()).toBe(200);
    expect(await rows<{ count: string }>(pool, tenantID, 'SELECT count(*)::text AS count FROM mcp_configs WHERE id=$1', [serverID]))
      .toEqual([{ count: '0' }]);
    completed.push('mcp.mutation.delete.mcp.servers.id.config');
    recordEvidence(evidence, 'MCP configuration deletion');
  } finally {
    await page.close();
  }
  return completed;
};
