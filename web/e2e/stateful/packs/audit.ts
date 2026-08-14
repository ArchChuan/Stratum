import { expect, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { requireUUID, withTenantQuery, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

interface AuditPackContext {
  actor: BrowserActor;
  member: BrowserActor;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  backendURL: string;
}

interface AuditRow {
  resource_kind: string;
  operation: string;
  actor_id: string;
  created_at: string;
}

// resource_change_audits 位于 tenant schema（非 public），withTenantQuery 的
// search_path 含 tenant_<id>，裸表名即解析到租户 schema，口径与后端 List 的
// COUNT 一致（tenant_id 谓词恒存在）。
const tenantRows = async <R extends QueryResultRow>(
  pool: DatabasePool,
  tenantID: string,
  text: string,
  values: unknown[],
): Promise<R[]> => (await withTenantQuery<R>(pool, tenantID, { text, values })).rows;

const auditCount = async (
  pool: DatabasePool,
  tenantID: string,
  where: string,
  values: unknown[],
): Promise<number> => {
  const rows = await tenantRows<{ total: string }>(pool, tenantID,
    `SELECT count(*)::text AS total FROM resource_change_audits WHERE ${where}`, values);
  return Number(rows[0].total);
};

// /audit 同时是 SPA 路由（导航返回 index.html），API 请求是 fetch/xhr，
// 必须按 resourceType 区分，否则会匹配到导航的 HTML 响应。
const waitForList = (page: Page) => page.waitForResponse((item) => {
  const path = new URL(item.url()).pathname;
  const type = item.request().resourceType();
  return path === '/audit/events' && (type === 'fetch' || type === 'xhr') && item.request().method() === 'GET';
});

// 点击「查询」并等待 /audit/events 响应，返回列表与 total。
const submitSearch = async (
  page: Page,
): Promise<{ status: number; rows: Array<Record<string, unknown>>; total: number }> => {
  const listResponse = waitForList(page);
  // antd Button autoInsertSpace 会把 2 汉字按钮渲染为「查 询」,用 regex 匹配任意空白。
  await page.getByRole('button', { name: /查\s*询/ }).click();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = await res.json() as { events: Array<Record<string, unknown>>; total: number };
  return { status: res.status(), rows: body.events, total: body.total };
};

// antd Select 可点击区域是 .ant-select-selector,通过 Form.Item label 过滤定位
// （getByLabel 对 antd Select 内部 input 关联不可靠,点击会超时）。
const pickResourceKind = async (page: Page, label: string) => {
  await page.locator('.ant-form-item').filter({ hasText: '资源类型' }).locator('.ant-select-selector').click();
  const dropdown = page.locator('.ant-select-dropdown:visible');
  await dropdown.getByText(label, { exact: true }).click();
};

// audit.route.audit：租户 owner/admin 打开 /audit，断言页面渲染，GET /audit/events
// 的 total 与 tenant schema resource_change_audits 该租户事件数对账。
const verifyOwnerVisible = async (
  page: Page, pool: DatabasePool, tenantID: string, evidence: EvidenceRecord, webURL: string,
) => {
  const listResponse = waitForList(page);
  await page.goto(`${webURL}/audit`);
  // 侧边菜单也有「审计日志」链接，getByText 会命中 2 个元素触发 strict mode violation，
  // 用 heading role 精确定位页面标题。
  await expect(page.getByRole('heading', { name: '审计日志' })).toBeVisible();
  const res = await listResponse;
  expect(res.status()).toBe(200);
  const body = await res.json() as { total: number };
  const dbTotal = await auditCount(pool, tenantID, 'tenant_id = $1', [tenantID]);
  expect(body.total).toBe(dbTotal);
  evidence.ui.push('Audit page rendered with filter and event table for a tenant owner');
  evidence.http.push('GET /audit/events returned 200 with paginated events');
  evidence.database.push('Audit event rows agree with the rendered list and total');
};

// 普通 member 前端路由守卫渲染 403，后端 RequireTenantRole("admin") 同样 403。
const verifyMemberDenied = async (
  page: Page, member: BrowserActor, backendURL: string, webURL: string, evidence: EvidenceRecord,
) => {
  await page.goto(`${webURL}/audit`);
  await expect(page.getByText('仅管理员可访问此页面，普通成员无权限。', { exact: false })).toBeVisible();
  const memberToken = member.accessToken ?? '';
  const denied = await page.request.get(`${backendURL}/audit/events`, {
    headers: { Authorization: `Bearer ${memberToken}` },
  });
  expect(denied.status()).toBe(403);
  evidence.ui.push('Audit page is denied for a plain tenant member');
  evidence.http.push('GET /audit/events returned 403 for a plain tenant member');
};

// 通过浏览器创建一条 workflow（POST /workflows 触发写端审计），返回 definition id，
// 供后续列表/筛选对账。
const createWorkflowForAudit = async (
  page: Page, pool: DatabasePool, tenantID: string, actorID: string, webURL: string,
): Promise<string> => {
  await page.goto(`${webURL}/workflows/new`);
  await expect(page.getByRole('region', { name: '工作流画布' })).toBeVisible();
  const createResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/workflows'
    && response.request().method() === 'POST'
  ));
  await page.getByLabel('工作流名称').fill(`E2E-Audit-${Date.now()}`);
  // 「任务名称」为必填（否则 form.validateFields 失败,保存直接 return 不发请求）;
  // 且 CreateWorkflowRequest.spec 为 required,空画布 spec 为空数组会 400,故加一个节点。
  await page.getByLabel('任务名称').fill('审计验收');
  await page.getByRole('button', { name: '添加人工审批节点' }).click();
  await page.getByRole('button', { name: '保存草稿' }).click();
  const created = await createResponse;
  expect(created.status()).toBe(201);
  const definitionID = requireUUID((await created.json() as { id: string }).id, 'workflow_definition_id');
  const rows = await tenantRows<AuditRow>(pool, tenantID,
    `SELECT resource_kind, operation, actor_id,
       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS created_at
     FROM resource_change_audits WHERE resource_kind = 'workflow' AND resource_id = $1`, [definitionID]);
  expect(rows).toHaveLength(1);
  expect(rows[0].operation).toBe('create');
  expect(rows[0].actor_id).toBe(actorID);
  return definitionID;
};

const assertContains = (
  rows: Array<Record<string, unknown>>,
  definitionID: string,
  operation: string,
  kind: string,
) => {
  expect(rows.some((row) => (
    row.resource_id === definitionID && row.operation === operation && row.resource_kind === kind
  )), 'filtered audit list must contain the created workflow row').toBe(true);
};

// antd RangePicker（showTime）的本地时间输入格式：YYYY-MM-DD HH:mm:ss。
const formatLocal = (date: Date): string => {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} `
    + `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
};

// 三种筛选（资源类型/操作者/时间范围）均通过页面表单控件操作，各自与 DB 计数
// 对账，并确认刚创建的 workflow 行出现在筛选结果中。
const verifyFilters = async (
  page: Page, pool: DatabasePool, tenantID: string, actorID: string, definitionID: string,
  createdRows: AuditRow[], evidence: EvidenceRecord,
) => {
  const { created_at } = createdRows[0];

  // 资源类型：Select 选「工作流」后查询。
  await pickResourceKind(page, '工作流');
  const kindRes = await submitSearch(page);
  const kindTotal = await auditCount(pool, tenantID,
    "tenant_id = $1 AND resource_kind = 'workflow'", [tenantID]);
  expect(kindRes.total).toBe(kindTotal);
  assertContains(kindRes.rows, definitionID, 'create', 'workflow');

  // 操作者：actor_name 兜底为 actor_id（e2e actor 无 display_name/github_login），
  // 输入 actorID 模糊匹配即命中。同样按 Form.Item label 过滤定位（getByLabel 不可靠）。
  await page.locator('.ant-form-item').filter({ hasText: '操作者' }).locator('input').fill(actorID);
  const actorRes = await submitSearch(page);
  const actorTotal = await auditCount(pool, tenantID, 'tenant_id = $1 AND actor_id = $2', [tenantID, actorID]);
  expect(actorRes.total).toBe(actorTotal);
  assertContains(actorRes.rows, definitionID, 'create', 'workflow');

  // 时间范围：RangePicker 两个输入框键入本地时间后 Enter（antd 解析为 dayjs，
  // onSearch 统一转 RFC3339 传给后端）。
  const target = new Date(created_at);
  const from = new Date(target.getTime() - 60_000);
  const to = new Date(target.getTime() + 60_000);
  const rangeInputs = page.locator('.ant-picker-range .ant-picker-input input');
  await rangeInputs.nth(0).fill(formatLocal(from));
  await rangeInputs.nth(0).press('Enter');
  await rangeInputs.nth(1).fill(formatLocal(to));
  await rangeInputs.nth(1).press('Enter');
  const timeRes = await submitSearch(page);
  const timeTotal = await auditCount(pool, tenantID,
    'tenant_id = $1 AND created_at >= $2 AND created_at <= $3',
    [tenantID, from.toISOString(), to.toISOString()]);
  expect(timeRes.total).toBe(timeTotal);
  assertContains(timeRes.rows, definitionID, 'create', 'workflow');

  evidence.http.push('Audit filters returned totals consistent with the tenant audit table');
  evidence.database.push('Audit filters (resource_kind/actor_name/time range) reconciled against the tenant audit table');
};

export const executeAuditPack = async ({
  actor, member, pool, evidence, webURL, backendURL,
}: AuditPackContext): Promise<string[]> => {
  const tenantID = requireUUID(actor.tenantID ?? '', 'tenant_id');
  const actorID = requireUUID(actor.userID ?? '', 'user_id');
  const page = await actor.context.newPage();
  try {
    await verifyOwnerVisible(page, pool, tenantID, evidence, webURL);

    // 创建 workflow 草稿触发写端审计后，验证列表出现 create 行并筛选对账。
    const definitionID = await createWorkflowForAudit(page, pool, tenantID, actorID, webURL);
    const createdRows = await tenantRows<AuditRow>(pool, tenantID,
      `SELECT resource_kind, operation, actor_id,
         to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS created_at
       FROM resource_change_audits WHERE resource_kind = 'workflow' AND resource_id = $1`, [definitionID]);
    // 创建草稿后已导航到 /workflows/{id}/edit,先回到 /audit 列表页再验证筛选。
    await page.goto(`${webURL}/audit`);
    await expect(page.getByRole('heading', { name: '审计日志' })).toBeVisible();
    await verifyFilters(page, pool, tenantID, actorID, definitionID, createdRows, evidence);

    // 普通 member 无权限：前端 403 + 后端 API 403。
    const memberPage = await member.context.newPage();
    try {
      await verifyMemberDenied(memberPage, member, backendURL, webURL, evidence);
    } finally {
      await memberPage.close();
    }
    return ['audit.route.audit'];
  } finally {
    await page.close();
  }
};
