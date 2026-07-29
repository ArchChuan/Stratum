import { expect } from '@playwright/test';

import { withTenantQuery } from '../core/database';
import type { BrowserAction } from './types';

export const dashboardActions: BrowserAction[] = [{
  id: 'dashboard.summary.refresh',
  execute: async ({ page, evidence, pool, tenantID, webURL }) => {
    let overviewResponses = 0;
    page.on('response', (response) => {
      const path = new URL(response.url()).pathname;
      if (
        response.request().method() === 'GET'
        && path === '/dashboard/overview'
        && response.ok()
      ) overviewResponses += 1;
    });
    await page.goto(`${webURL}/`);
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();
    await expect(page.locator('.ant-skeleton')).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();
    await expect(page.locator('.ant-skeleton')).toHaveCount(0);

    const expected = [
      ['Agent', 'SELECT count(*)::text AS count FROM agents'],
      ['技能', 'SELECT count(*)::text AS count FROM skills'],
      ['知识库', 'SELECT count(*)::text AS count FROM rag_workspaces'],
      ['MCP 服务器', 'SELECT count(*)::text AS count FROM mcp_configs'],
      ['模型厂商', 'SELECT count(*)::text AS count FROM providers'],
      ['租户成员', 'SELECT count(*)::text AS count FROM public.tenant_members WHERE tenant_id = $1'],
      ['工作流', 'SELECT count(*)::text AS count FROM workflow_definitions'],
      ['近七日 Agent 对话', `SELECT count(*)::text AS count FROM chat_messages
        WHERE role = 'user' AND created_at >= NOW() - INTERVAL '168 hours'`],
    ] as const;
    for (const [label, query] of expected) {
      const result = await withTenantQuery<{ count: string }>(pool, tenantID, {
        text: query,
        values: label === '租户成员' ? [tenantID] : [],
      });
      const card = page.locator('.ant-card').filter({ has: page.getByText(label, { exact: true }) });
      await expect(card).toContainText(result.rows[0].count);
      evidence.database.push(`${label} count reconciled`);
    }
    expect(overviewResponses).toBeGreaterThanOrEqual(2);
    evidence.ui.push('dashboard summary remained visible after refresh');
    evidence.http.push('dashboard overview request completed on initial load and refresh');
  },
}];
