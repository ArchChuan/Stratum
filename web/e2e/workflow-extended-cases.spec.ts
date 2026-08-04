import { test, expect, request, type BrowserContext, type APIRequestContext } from '@playwright/test';
import { createRealSession, queryTenant } from './support/real-workflow';

const API_BASE = 'http://localhost:8080';
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

interface WorkflowRef {
  definitionId: string;
  versionId: string;
  name: string;
}

/** 通过 refresh cookie 换 access token —— 与浏览器会话同一租户同一用户 */
const sessionToken = async (context: BrowserContext): Promise<string> => {
  const res = await context.request.post(`${API_BASE}/auth/refresh`);
  expect(res.status(), 'refresh must succeed').toBe(200);
  const { access_token: token } = await res.json() as { access_token: string };
  return token;
};

/** 独立 API context(baseURL 指向后端,与浏览器会话无关) */
const apiFor = () => request.newContext({ baseURL: API_BASE });

/** 独立 API 会话:guest → 提升 admin → refresh → token */
const newSessionApi = async (role: 'admin' | 'member') => {
  const api = await apiFor();
  const guest = await api.post('/auth/guest');
  expect(guest.status()).toBe(201);
  const body = await guest.json() as { tenant_id: string; user: { sub: string } };
  const { tenant_id: tenantId, user: { sub: userId } } = body;
  expect(tenantId).toMatch(uuidPattern);
  expect(userId).toMatch(uuidPattern);
  if (role === 'admin') {
    expect(queryTenant(tenantId, `UPDATE public.tenant_members SET role='admin' WHERE tenant_id='${tenantId}' AND user_id='${userId}' RETURNING 1`))
      .toContain('1');
  }
  const refresh = await api.post('/auth/refresh');
  expect(refresh.status()).toBe(200);
  const { access_token: accessToken } = await refresh.json() as { access_token: string };
  return { api, tenantId, accessToken };
};

const auth = (token: string) => ({ Authorization: `Bearer ${token}` });

/** 用指定 token 创建并发布一个自定义 spec 工作流 */
const publishSpec = async (api: APIRequestContext, token: string, name: string, spec: Record<string, unknown>) => {
  const create = await api.post('/workflows', {
    headers: auth(token),
    data: {
      name,
      description: '扩展 E2E 用例',
      spec,
      input_schema: { task_label: '审批事项', task_description: '请输入待审批事项', fields: [] },
    },
  });
  expect(create.status(), `create ${name}`).toBe(201);
  const definition = await create.json() as { id: string };
  const publish = await api.post(`/workflows/${definition.id}/publish`, { headers: auth(token) });
  expect(publish.status(), `publish ${name}`).toBe(201);
  const version = await publish.json() as { id: string };
  return { definitionId: definition.id, versionId: version.id, name } satisfies WorkflowRef;
};

const approvalNode = (id: string, name: string) => ({
  id, name, type: 'approval', agent_id: '', input_mapping: {}, output_mapping: {},
  retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
});

/** 启动 run,返回 {runId, created} */
const startRun = async (api: APIRequestContext, token: string, versionId: string, idempotencyKey?: string) => {
  const res = await api.post('/workflow-runs', {
    headers: auth(token),
    data: { version_id: versionId, task: 'E2E 扩展用例审批事项', idempotency_key: idempotencyKey ?? `run-${Date.now()}-${Math.random()}` },
  });
  expect([200, 202], `start run`).toContain(res.status());
  const body = await res.json() as { run_id: string; status: string };
  return { runId: body.run_id, created: res.status() === 202 };
};

const getRun = async (api: APIRequestContext, token: string, runId: string) => {
  const res = await api.get(`/workflow-runs/${runId}`, { headers: auth(token) });
  expect(res.status()).toBe(200);
  // 响应为 {run: {...}, approvals, ...} 嵌套形状
  const body = await res.json() as { run: { status: string; generation: number } };
  return body.run;
};

const listPendingApprovals = async (api: APIRequestContext, token: string, runId: string) => {
  const res = await api.get(`/workflow-approvals?pending=true&run_id=${runId}`, { headers: auth(token) });
  expect(res.status()).toBe(200);
  // 响应为 {approvals: [...]} 嵌套形状
  // wire 格式是 Go 大写字段,前端 schema 负责转换;这里直接消费 wire
  const body = await res.json() as { approvals: Array<{
    ID: string; RunID: string; AttemptID: string; RunGeneration: number; Status: string;
  }> | null };
  return body.approvals ?? [];
};

const decideApproval = async (api: APIRequestContext, token: string, approval: Awaited<ReturnType<typeof listPendingApprovals>>[number], decision: 'approve' | 'reject') => {
  const res = await api.post(`/workflow-approvals/${approval.ID}/decision`, {
    headers: auth(token),
    data: {
      run_id: approval.RunID,
      attempt_id: approval.AttemptID,
      expected_generation: approval.RunGeneration,
      decision,
      comment: decision === 'reject' ? 'E2E 拒绝测试' : 'E2E 批准测试',
    },
  });
  expect(res.status(), `decision ${decision}`).toBe(200);
};

const resumeRun = async (api: APIRequestContext, token: string, runId: string, generation: number) => {
  const res = await api.post(`/workflow-runs/${runId}/resume`, {
    headers: auth(token),
    data: { expected_generation: generation },
  });
  expect(res.status(), `resume`).toBe(202);
};

/** 轮询 DB 直到 run 到达期望状态 */
const waitRunStatus = async (tenantId: string, runId: string, status: string, timeout = 45000) => {
  await expect.poll(
    () => queryTenant(tenantId, `SELECT status FROM workflow_runs WHERE id='${runId}'`),
    { timeout, message: `run ${runId} should reach ${status}` },
  ).toBe(status);
};

const runEvents = (tenantId: string, runId: string) =>
  queryTenant(tenantId, `SELECT string_agg(event_type, ',' ORDER BY sequence_no) FROM workflow_events WHERE run_id='${runId}'`);

/** 在页面里点击 AntD Modal.confirm 的 primary 按钮 */
const confirmDialog = async (page: import('@playwright/test').Page) => {
  await page.locator('.ant-modal-confirm-btns .ant-btn-primary').click();
};

/** 用例内按 project 名条件跳过 —— describe 级 test.skip 回调在该版本签名不稳定 */
const skipUnless = (testInfo: import('@playwright/test').TestInfo, project: string | ((name: string) => boolean), why: string) => {
  const ok = typeof project === 'string' ? testInfo.project.name === project : project(testInfo.project.name);
  if (!ok) test.skip(true, why);
};

test.describe('工作流扩展 E2E 用例', () => {
  test.describe('桌面视口', () => {
    test.use({ viewport: { width: 1440, height: 900 } });

    test('审批拒绝闭环:拒绝 → 继续 → run failed,approval rejected', async ({ page, context }, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const session = await createRealSession(context, 'admin');
      const token = await sessionToken(context);
      const api = await apiFor();
      const { tenantId } = session;
      const workflow = await publishSpec(api, token, `E2E-拒绝闭环-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });

      const { runId } = await startRun(api, token, workflow.versionId);
      await waitRunStatus(tenantId, runId, 'paused');

      // admin UI 打开 run 详情,出现审批面板
      await page.goto(`/workflow-runs/${runId}`);
      const rejectButton = page.getByRole('button', { name: /拒\s*绝/ });
      await expect(rejectButton).toBeVisible({ timeout: 15000 });

      // 点击拒绝 → 确认弹窗 → 确认
      await rejectButton.click();
      await expect(page.locator('.ant-modal-confirm-title')).toHaveText(/拒绝/);
      await confirmDialog(page);
      // 拒绝即终态:store 在 decision 事务内同步把 run 置为 failed(拒绝并终止当前步骤)
      await waitRunStatus(tenantId, runId, 'failed');
      // 审批面板消失(approval 已决定)
      await expect(rejectButton).toBeHidden({ timeout: 15000 });

      // DB 三方证据
      expect(queryTenant(tenantId, `SELECT status FROM workflow_approvals WHERE run_id='${runId}'`)).toBe('rejected');
      const events = runEvents(tenantId, runId);
      expect(events).toContain('workflow.approval_decided');
      expect(events).toContain('workflow.run_failed');

      // UI 终态:刷新后审批面板不再出现
      await page.reload();
      await expect(rejectButton).toBeHidden({ timeout: 15000 });
    });

    test('审批通过闭环:批准 → 继续 → run completed,approval approved', async ({ page, context }, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const session = await createRealSession(context, 'admin');
      const token = await sessionToken(context);
      const api = await apiFor();
      const { tenantId } = session;
      const workflow = await publishSpec(api, token, `E2E-通过闭环-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });

      const { runId } = await startRun(api, token, workflow.versionId);
      await waitRunStatus(tenantId, runId, 'paused');

      await page.goto(`/workflow-runs/${runId}`);
      const approveButton = page.getByRole('button', { name: /批\s*准/ });
      await expect(approveButton).toBeVisible({ timeout: 15000 });
      await approveButton.click();
      await expect(page.locator('.ant-modal-confirm-title')).toHaveText(/批准/);
      await confirmDialog(page);
      await expect(approveButton).toBeHidden({ timeout: 15000 });

      const resumeButton = page.getByRole('button', { name: /继\s*续/ });
      await expect(resumeButton).toBeVisible({ timeout: 15000 });
      await resumeButton.click();
      await confirmDialog(page);

      await waitRunStatus(tenantId, runId, 'completed');

      // DB:approval approved,finished_at 已写,事件链完整
      expect(queryTenant(tenantId, `SELECT status FROM workflow_approvals WHERE run_id='${runId}'`)).toBe('approved');
      expect(queryTenant(tenantId, `SELECT finished_at IS NOT NULL FROM workflow_runs WHERE id='${runId}'`)).toBe('t');
      const events = runEvents(tenantId, runId);
      expect(events).toContain('workflow.approval_decided');
      expect(events).toContain('workflow.resumed');
      expect(events).toContain('workflow.run_completed');
      expect(queryTenant(tenantId, `SELECT count(*) FROM workflow_node_attempts WHERE run_id='${runId}'`)).toBe('1');

      await page.reload();
      await expect(approveButton).toBeHidden({ timeout: 15000 });
    });

    test('持久化刷新:reload 后 run 仍 paused,审批面板仍在', async ({ page, context }, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const session = await createRealSession(context, 'admin');
      const token = await sessionToken(context);
      const api = await apiFor();
      const { tenantId } = session;
      const workflow = await publishSpec(api, token, `E2E-刷新-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });

      const { runId } = await startRun(api, token, workflow.versionId);
      await waitRunStatus(tenantId, runId, 'paused');

      await page.goto(`/workflow-runs/${runId}`);
      await expect(page.getByRole('button', { name: /批\s*准/ })).toBeVisible({ timeout: 15000 });

      // 刷新两次,持久化状态不变
      await page.reload();
      await expect(page.getByRole('button', { name: /批\s*准/ })).toBeVisible({ timeout: 15000 });
      await page.reload();
      await expect(page.getByRole('button', { name: /批\s*准/ })).toBeVisible({ timeout: 15000 });

      expect(queryTenant(tenantId, `SELECT status FROM workflow_runs WHERE id='${runId}'`)).toBe('paused');
    });

    test('权限矩阵(API):member 写操作 403,admin 放行,member 启动/取消放行', async ({}, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const member = await newSessionApi('member');
      const admin = await newSessionApi('admin');
      const { api: adminApi, tenantId, accessToken: adminToken } = admin;
      const workflow = await publishSpec(adminApi, adminToken, `E2E-权限-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });
      const { runId } = await startRun(adminApi, adminToken, workflow.versionId);
      await waitRunStatus(tenantId, runId, 'paused');
      const approvals = await listPendingApprovals(adminApi, adminToken, runId);
      expect(approvals.length).toBeGreaterThan(0);

      const { api: memberApi, accessToken: memberToken } = member;
      const forbidden: Array<() => Promise<{ status: number; body: unknown }>> = [
        async () => { const r = await memberApi.post('/workflows', { headers: auth(memberToken), data: {} }); return { status: r.status(), body: await r.json().catch(() => null) }; },
        async () => { const r = await memberApi.post(`/workflows/${workflow.definitionId}/validate`, { headers: auth(memberToken) }); return { status: r.status(), body: await r.json().catch(() => null) }; },
        async () => { const r = await memberApi.post(`/workflows/${workflow.definitionId}/publish`, { headers: auth(memberToken) }); return { status: r.status(), body: await r.json().catch(() => null) }; },
        async () => { const r = await memberApi.post(`/workflow-runs/${runId}/resume`, { headers: auth(memberToken), data: { expected_generation: 1 } }); return { status: r.status(), body: await r.json().catch(() => null) }; },
        async () => { const r = await memberApi.post(`/workflow-runs/${runId}/pause`, { headers: auth(memberToken), data: { reason: 'x' } }); return { status: r.status(), body: await r.json().catch(() => null) }; },
        async () => { const r = await memberApi.post(`/workflow-approvals/${approvals[0].ID}/decision`, { headers: auth(memberToken), data: {} }); return { status: r.status(), body: await r.json().catch(() => null) }; },
      ];
      for (const call of forbidden) {
        const { status, body } = await call();
        expect(status, `member write must be forbidden`).toBe(403);
        // RBAC 中间件 403 响应体是 {code, message},不是 {error}
        expect((body as { message?: string }).message, '403 body must expose message').toBeTruthy();
      }

      // member 放行:启动 + 取消
      const memberStart = await memberApi.post('/workflow-runs', {
        headers: auth(memberToken),
        data: { version_id: workflow.versionId, task: 'member 启动', idempotency_key: `perm-${Date.now()}` },
      });
      expect(memberStart.status()).toBe(202);
      const { run_id: memberRunId } = await memberStart.json() as { run_id: string };
      // control 请求带 required expected_generation 乐观锁
      const memberRun = await getRun(memberApi, memberToken, memberRunId);
      const memberCancel = await memberApi.post(`/workflow-runs/${memberRunId}/cancel`, {
        headers: auth(memberToken),
        data: { expected_generation: memberRun.generation, reason: 'member 取消' },
      });
      // controlRun handler 恒返回 202(异步受理)
      expect(memberCancel.status()).toBe(202);

      // admin 对照:validate 放行
      const adminValidate = await adminApi.post(`/workflows/${workflow.definitionId}/validate`, { headers: auth(adminToken) });
      expect(adminValidate.status()).toBe(200);
    });

    test('幂等重放:同 version+key 两次启动 → 同 run_id;不同 key → 新 run', async ({}, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const admin = await newSessionApi('admin');
      const { api, tenantId, accessToken } = admin;
      const workflow = await publishSpec(api, accessToken, `E2E-幂等-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });
      const key = `idem-${Date.now()}`;

      const first = await startRun(api, accessToken, workflow.versionId, key);
      expect(first.created).toBe(true);
      const second = await startRun(api, accessToken, workflow.versionId, key);
      expect(second.created).toBe(false);
      expect(second.runId).toBe(first.runId);

      // 不同 key → 新 run
      const third = await startRun(api, accessToken, workflow.versionId, `idem-${Date.now()}`);
      expect(third.created).toBe(true);
      expect(third.runId).not.toBe(first.runId);

      // DB 计数一致(同 key 只落一行)
      expect(queryTenant(tenantId, `SELECT count(*) FROM workflow_runs WHERE id IN ('${first.runId}','${third.runId}')`)).toBe('2');
      await waitRunStatus(tenantId, first.runId, 'paused');
    });

    test('校验/发布门禁:非法图 validate 400,publish 400,DB 无 version;修复后可发布', async ({}, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const admin = await newSessionApi('admin');
      const { api, tenantId, accessToken } = admin;

      // 非法 spec:边指向不存在的节点
      const create = await api.post('/workflows', {
        headers: auth(accessToken),
        data: {
          name: `E2E-门禁-${Date.now()}`,
          description: '非法图',
          spec: {
            nodes: [approvalNode('approval-1', '管理员审批')],
            edges: [{ from: 'approval-1', to: 'ghost-node' }],
            max_concurrency: 0,
          },
          input_schema: { task_label: '审批事项', task_description: '', fields: [] },
        },
      });
      expect(create.status(), '创建不校验 spec,应允许落草稿').toBe(201);
      const { id: definitionId } = await create.json() as { id: string };

      const validate = await api.post(`/workflows/${definitionId}/validate`, { headers: auth(accessToken) });
      expect(validate.status()).toBe(400);
      expect(((await validate.json()) as { error?: string }).error, 'validate error body').toBeTruthy();

      const publish = await api.post(`/workflows/${definitionId}/publish`, { headers: auth(accessToken) });
      expect(publish.status()).toBe(400);

      // DB:无 version 落库
      expect(queryTenant(tenantId, `SELECT count(*) FROM workflow_versions WHERE definition_id='${definitionId}'`)).toBe('0');

      // 修复 spec → validate 200 → publish 201 → DB 有 version
      const fix = await api.put(`/workflows/${definitionId}/draft`, {
        headers: auth(accessToken),
        data: {
          name: `E2E-门禁-${Date.now()}`,
          description: '合法图',
          spec: { nodes: [approvalNode('approval-1', '管理员审批')], edges: [], max_concurrency: 0 },
          input_schema: { task_label: '审批事项', task_description: '', fields: [] },
          expected_revision: 1,
        },
      });
      expect(fix.status(), 'draft 修复').toBe(200);
      const validateAgain = await api.post(`/workflows/${definitionId}/validate`, { headers: auth(accessToken) });
      expect(validateAgain.status()).toBe(200);
      const publishAgain = await api.post(`/workflows/${definitionId}/publish`, { headers: auth(accessToken) });
      expect(publishAgain.status()).toBe(201);
      expect(queryTenant(tenantId, `SELECT count(*) FROM workflow_versions WHERE definition_id='${definitionId}'`)).toBe('1');
    });

    test('diamond 多节点闭环:approval 分叉 → 双分支 → join,4 个 approval 全部批准 → completed,4 attempts', async ({}, testInfo) => {
      skipUnless(testInfo, 'desktop-1440', '本组用例只在桌面视口执行');
      const admin = await newSessionApi('admin');
      const { api, tenantId, accessToken } = admin;
      const workflow = await publishSpec(api, accessToken, `E2E-Diamond-${Date.now()}`, {
        nodes: [
          approvalNode('approval-1', '分叉审批'),
          approvalNode('left-2', '左分支审批'),
          approvalNode('right-3', '右分支审批'),
          approvalNode('join-4', '汇聚审批'),
        ],
        edges: [
          { from: 'approval-1', to: 'left-2' },
          { from: 'approval-1', to: 'right-3' },
          { from: 'left-2', to: 'join-4' },
          { from: 'right-3', to: 'join-4' },
        ],
        max_concurrency: 0,
      });

      const { runId } = await startRun(api, accessToken, workflow.versionId);

      // 循环:审批全部 pending approval → 再 resume,直到终态
      for (let round = 0; round < 12; round++) {
        const run = await getRun(api, accessToken, runId);
        if (run.status === 'completed' || run.status === 'failed') break;
        // queued:worker 尚未把 run 推到审批暂停点,等 worker 处理后再轮询
        if (run.status === 'queued') {
          await new Promise((resolve) => setTimeout(resolve, 1500));
          continue;
        }
        const approvals = await listPendingApprovals(api, accessToken, runId);
        if (approvals.length > 0) {
          for (const a of approvals) {
            await decideApproval(api, accessToken, a, 'approve');
          }
        } else {
          await resumeRun(api, accessToken, runId, run.generation);
        }
      }

      await waitRunStatus(tenantId, runId, 'completed', 90000);
      const attempts = queryTenant(tenantId, `SELECT count(*) FROM workflow_node_attempts WHERE run_id='${runId}'`);
      expect(attempts).toBe('4');
      const events = runEvents(tenantId, runId);
      expect(events).toContain('workflow.run_completed');
      expect(events.split(',').length).toBeGreaterThanOrEqual(8);
      // 4 个节点均为 approval,每个 1 个 approved 行;left-2/right-3 并行分支的 run_generation 必须一致(6)
      const approved = queryTenant(tenantId, `SELECT count(*) FROM workflow_approvals WHERE run_id='${runId}' AND status='approved'`);
      expect(approved).toBe('4');
      const gens = queryTenant(tenantId, `SELECT string_agg(run_generation::text, ',' ORDER BY node_id) FROM workflow_approvals WHERE run_id='${runId}' AND status='approved'`);
      expect(gens).toBe('3,9,6,6');
    });
  });

  test.describe('移动端视口', () => {
    test.use({ viewport: { width: 390, height: 844 } });

    test('run 取消闭环(移动端):取消运行 → canceled', async ({ page, context }, testInfo) => {
      skipUnless(testInfo, (name) => name.startsWith('mobile'), '本组用例只在移动视口执行');
      const session = await createRealSession(context, 'admin');
      const token = await sessionToken(context);
      const api = await apiFor();
      const { tenantId } = session;
      const workflow = await publishSpec(api, token, `E2E-取消闭环-${Date.now()}`, {
        nodes: [approvalNode('approval-1', '管理员审批')],
        edges: [],
        max_concurrency: 0,
      });

      const { runId } = await startRun(api, token, workflow.versionId);
      await waitRunStatus(tenantId, runId, 'paused');

      await page.goto(`/workflow-runs/${runId}`);
      const cancelButton = page.getByRole('button', { name: '取消运行' });
      await expect(cancelButton).toBeVisible({ timeout: 15000 });
      await cancelButton.click();
      await confirmDialog(page);

      await waitRunStatus(tenantId, runId, 'canceled');
      const events = runEvents(tenantId, runId);
      expect(events).toContain('workflow.cancel_requested');

      await page.reload();
      await expect(cancelButton).toBeHidden({ timeout: 15000 });
    });
  });
});
