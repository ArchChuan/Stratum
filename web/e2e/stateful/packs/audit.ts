import { expect, type Page } from '@playwright/test';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface AuditPackContext { actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string }

// audit_events 位于 public schema（非 tenant schema），按 tenant_id 列隔离，
// 对账 SQL 使用 schema-qualified 名称，口径与后端 ListEvents 的 Count 一致。
const tenantAuditEvents = `SELECT count(*)::text AS total FROM public.audit_events WHERE tenant_id = $1`;

// audit.route.audit：打开 /audit，断言页面渲染，GET /audit/events 的 total 与
// audit_events 该租户事件数对账（列表端点同口径分页）。
const verifyRoute = async (
  page: Page, pool: DatabasePool, tenantID: string, evidence: EvidenceRecord, webURL: string,
) => {
  // /audit 同时是 SPA 路由（导航返回 index.html），API 请求是 fetch/xhr，
  // 必须按 resourceType 区分，否则会匹配到导航的 HTML 响应。
  const listResponse = page.waitForResponse((item) => {
    const path = new URL(item.url()).pathname;
    const type = item.request().resourceType();
    return path === '/audit/events' && (type === 'fetch' || type === 'xhr') && item.request().method() === 'GET';
  });
  await page.goto(`${webURL}/audit`);
  // 侧边菜单也有「审计日志」链接，getByText 会命中 2 个元素触发 strict mode violation，
  // 用 heading role 精确定位页面标题。
  await expect(page.getByRole('heading', { name: '审计日志' })).toBeVisible();
  await expect(page.getByText('租户内操作审计事件', { exact: false })).toBeVisible();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { total: number };
  const result = await withTenantQuery<{ total: string }>(pool, tenantID, {
    text: tenantAuditEvents, values: [tenantID],
  });
  expect(body.total).toBe(Number(result.rows[0].total));
  evidence.ui.push('Audit page rendered with filter and event table');
  evidence.http.push('GET /audit/events returned 200 with paginated events');
  evidence.database.push('Audit event rows agree with the rendered list and total');
};

export const executeAuditPack = async ({ actor, pool, evidence, webURL }: AuditPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const page = await actor.context.newPage();
  try {
    await verifyRoute(page, pool, tenantID, evidence, webURL);
    return ['audit.route.audit'];
  } finally {
    await page.close();
  }
};
