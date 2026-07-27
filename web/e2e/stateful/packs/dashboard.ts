import { expect } from '@playwright/test';

import { withTenantQuery } from '../core/database';
import type { BrowserAction } from './types';

export const dashboardActions: BrowserAction[] = [{
  id: 'dashboard.summary.refresh',
  execute: async ({ page, evidence, pool, tenantID, webURL }) => {
    const responses = new Set<string>();
    page.on('response', (response) => {
      const path = new URL(response.url()).pathname;
      if (
        response.request().method() === 'GET'
        && ['/agents', '/skills', '/mcp/servers', '/knowledge/workspaces'].includes(path)
        && response.ok()
      ) responses.add(path);
    });
    await page.goto(`${webURL}/`);
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();
    await expect(page.locator('.ant-skeleton')).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();
    await expect(page.locator('.ant-skeleton')).toHaveCount(0);

    const expected = [
      ['Agent', 'agents'], ['技能', 'skills'], ['MCP 服务器', 'mcp_configs'], ['知识库', 'rag_workspaces'],
    ] as const;
    for (const [label, table] of expected) {
      const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
        text: `SELECT count(*)::text AS count FROM ${table}`,
        values: [],
      });
      const card = page.locator('.ant-card').filter({ hasText: label });
      await expect(card).toContainText(result.rows[0].count);
      evidence.database.push(`${table} count reconciled`);
    }
    expect(responses).toEqual(new Set(['/agents', '/skills', '/mcp/servers', '/knowledge/workspaces']));
    evidence.ui.push('dashboard summary remained visible after refresh');
    evidence.http.push('dashboard list requests completed twice');
  },
}];
