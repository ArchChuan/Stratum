import { expect, request, test } from '@playwright/test';

import { createRealSession, queryTenant } from './support/real-workflow';

test.skip(process.env.REAL_E2E !== '1', 'set REAL_E2E=1 to run against the local backend and database');

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

/** 在画布内从 source handle 拖到 target handle（React Flow v12 用 pointer 事件，mouse 输入可触发） */
const connectHandles = async (page: import('@playwright/test').Page, from: import('@playwright/test').Locator, to: import('@playwright/test').Locator) => {
  const source = await from.boundingBox();
  const target = await to.boundingBox();
  if (!source || !target) throw new Error('handle not visible');
  await page.mouse.move(source.x + source.width / 2, source.y + source.height / 2);
  await page.mouse.down();
  await page.mouse.move(target.x + target.width / 2, target.y + target.height / 2, { steps: 12 });
  await page.mouse.up();
};

/** 拖动节点到画布内偏移位置（React Flow 节点拖拽同走 pointer 事件） */
const dragNodeBy = async (page: import('@playwright/test').Page, node: import('@playwright/test').Locator, dx: number, dy: number) => {
  const box = await node.boundingBox();
  if (!box) throw new Error('node not visible');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { steps: 10 });
  await page.mouse.up();
};

const waitForDraft = async (page: import('@playwright/test').Page) => {
  const response = page.waitForResponse((r) => r.url().endsWith('/workflows') && r.request().method() === 'POST', { timeout: 15000 });
  await page.getByRole('button', { name: '保存草稿' }).click();
  const created = await response;
  expect([200, 201]).toContain(created.status());
  return (await created.json()) as { id: string; revision: number };
};

test.describe('工作流画布可用化', () => {
  test('palette 拖拽插入节点并保存', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    const session = await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await expect(page.getByRole('region', { name: '工作流画布' })).toBeVisible();

    await page.getByRole('button', { name: '添加Agent节点' }).dragTo(page.getByRole('region', { name: '工作流画布' }));
    await expect(page.locator('.react-flow__node')).toHaveCount(1);
    await page.getByRole('button', { name: '添加条件判断节点' }).dragTo(page.getByRole('region', { name: '工作流画布' }));
    await expect(page.locator('.react-flow__node')).toHaveCount(2);
    // 拖入的节点互不重叠：两次 drop 的屏幕坐标不同，flow 坐标必须不同
    const transforms = await page.locator('.react-flow__node').evaluateAll((nodes) => nodes.map((node) => (node as HTMLElement).style.transform));
    expect(new Set(transforms).size).toBe(2);

    const { id } = await waitForDraft(page);
    expect(queryTenant(session.tenantId, `SELECT count(*) FROM workflow_definitions WHERE id='${id}'`)).toContain('1');
  });

  test('拖动节点后保存、重载坐标不丢失（DOM transform + SQL 双断言）', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    const session = await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await page.getByRole('button', { name: '添加Agent节点' }).click();
    const node = page.locator('.react-flow__node').first();
    await expect(node).toBeVisible();
    await dragNodeBy(page, node, 160, 120);
    const before = await node.getAttribute('style');

    const { id } = await waitForDraft(page);
    await page.reload();
    const reloaded = page.locator('.react-flow__node').first();
    await expect(reloaded).toBeVisible();
    // fitView 只作用于视口 wrapper，节点自身 transform 是 flow 坐标：重载后必须一致
    expect(await reloaded.getAttribute('style')).toBe(before);
    // JSONB 落库断言：position 对象已写入 draft_spec_json
    expect(queryTenant(session.tenantId,
      `SELECT draft_spec_json::text LIKE '%"position":{"x":%' FROM workflow_definitions WHERE id='${id}'`,
    )).toContain('t');
  });

  test('普通连线保存后重载边仍在且不派生 sourceHandle', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    const session = await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await page.getByRole('button', { name: '添加Agent节点' }).click();
    await page.getByRole('button', { name: '添加人工审批节点' }).click();
    await expect(page.locator('.react-flow__node')).toHaveCount(2);
    const source = page.locator('.react-flow__node').first().locator('.react-flow__handle.source');
    const target = page.locator('.react-flow__node').nth(1).locator('.react-flow__handle.target');
    await connectHandles(page, source, target);
    await expect(page.locator('.react-flow__edge')).toHaveCount(1);
    const { id } = await waitForDraft(page);

    await page.reload();
    await expect(page.locator('.react-flow__edge')).toHaveCount(1);
    // spec 落库：边 from/to 存在，且非 condition 边不含 sourceHandle 概念（JSONB 无该键）
    expect(queryTenant(session.tenantId,
      `SELECT draft_spec_json::text FROM workflow_definitions WHERE id='${id}'`,
    )).toMatch(/"from":"node-1".*"to":"node-2"|"to":"node-2".*"from":"node-1"/);
    expect(queryTenant(session.tenantId,
      `SELECT draft_spec_json::text FROM workflow_definitions WHERE id='${id}'`,
    )).not.toContain('sourceHandle');
  });

  test('条件节点三分支连线（是/否/默认）保存重载不变', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    const session = await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await page.getByRole('button', { name: '添加条件判断节点' }).click();
    await page.getByRole('button', { name: '添加Agent节点' }).click();
    await page.getByRole('button', { name: '添加人工审批节点' }).click();
    const condition = page.locator('.react-flow__node').filter({ hasText: '条件判断' }).first();
    const agent = page.locator('.react-flow__node').filter({ hasText: 'Agent' }).first();
    const approval = page.locator('.react-flow__node').filter({ hasText: '人工审批' }).first();
    await expect(condition).toBeVisible();

    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="yes"]'), agent.locator('.react-flow__handle.target'));
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="no"]'), approval.locator('.react-flow__handle.target'));
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="default"]'), agent.locator('.react-flow__handle.target'));
    await expect(page.locator('.react-flow__edge')).toHaveCount(3);
    await expect(page.locator('.react-flow__edge')).toContainText('是');
    await expect(page.locator('.react-flow__edge')).toContainText('否');
    await expect(page.locator('.react-flow__edge')).toContainText('默认');

    const { id } = await waitForDraft(page);
    await page.reload();
    await expect(page.locator('.react-flow__edge')).toHaveCount(3);
    const sql = queryTenant(session.tenantId,
      `SELECT draft_spec_json::text FROM workflow_definitions WHERE id='${id}'`,
    );
    expect(sql).toContain('"condition_value":true');
    expect(sql).toContain('"condition_value":false');
    expect(sql).toContain('"default":true');
  });

  test('inspector 映射编辑保存后重载保持一致', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    const session = await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await page.getByRole('button', { name: '添加Agent节点' }).click();
    await page.locator('.react-flow__node').first().click();
    await page.getByRole('button', { name: '高级设置' }).click();
    await page.getByLabel('输入映射').fill('{\n  "topic": "input"\n}');
    await page.getByLabel('输出映射').fill('{\n  "summary": "nodes.agent-1.output"\n}');
    const { id } = await waitForDraft(page);

    await page.reload();
    await page.locator('.react-flow__node').first().click();
    await page.getByRole('button', { name: '高级设置' }).click();
    await expect(page.getByLabel('输入映射')).toHaveValue(JSON.stringify({ topic: 'input' }, null, 2));
    await expect(page.getByLabel('输出映射')).toHaveValue(JSON.stringify({ summary: 'nodes.agent-1.output' }, null, 2));
    // JSONB 落库断言：mapping 以结构化对象持久化（非展平文本、非丢失）
    expect(queryTenant(session.tenantId,
      `SELECT draft_spec_json::text LIKE '%"input_mapping":{"topic":"input"}%' FROM workflow_definitions WHERE id='${id}'`,
    )).toContain('t');
  });

  test('condition→approval 执行链：输入命中是分支后暂停审批', async () => {
    const api = await request.newContext({ baseURL: 'http://localhost:8080' });
    try {
      const guest = await api.post('/auth/guest');
      const body = await guest.json() as { tenant_id: string; user: { sub: string } };
      expect(body.tenant_id).toMatch(uuidPattern);
      expect(queryTenant(body.tenant_id,
        `UPDATE public.tenant_members SET role='admin' WHERE tenant_id='${body.tenant_id}' AND user_id='${body.user.sub}' RETURNING 1`,
      )).toContain('1');
      const refresh = await api.post('/auth/refresh');
      const { access_token: accessToken } = await refresh.json() as { access_token: string };

      const name = `E2E-分支执行-${Date.now()}`;
      const spec = {
        nodes: [
          { id: 'node-condition', name: '分支判断', type: 'condition', agent_id: '', condition: "input.topic == 'x'", input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0 },
          { id: 'node-approval', name: '终审确认', type: 'approval', agent_id: '', input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0 },
        ],
        edges: [
          { id: 'edge-yes', from: 'node-condition', to: 'node-approval', condition_value: true, default: false },
          { id: 'edge-default', from: 'node-condition', to: 'node-approval', condition_value: null, default: true },
        ],
        max_concurrency: 0,
      };
      const create = await api.post('/workflows', {
        headers: { Authorization: `Bearer ${accessToken}` },
        data: {
          name, description: '画布分支执行验收',
          spec,
          input_schema: {
            task_label: '执行主题', task_description: '输入主题字段', fields: [
              { key: 'topic', label: '主题', type: 'text', required: true },
            ],
          },
        },
      });
      expect(create.status()).toBe(201);
      const definition = await create.json() as { id: string };
      const validate = await api.post(`/workflows/${definition.id}/validate`, { headers: { Authorization: `Bearer ${accessToken}` } });
      expect(validate.status()).toBe(200);
      const publish = await api.post(`/workflows/${definition.id}/publish`, { headers: { Authorization: `Bearer ${accessToken}` } });
      expect(publish.status()).toBe(201);
      const version = await publish.json() as { id: string };

      const start = await api.post('/workflow-runs', {
        headers: { Authorization: `Bearer ${accessToken}` },
        data: { version_id: version.id, task: '分支验收', fields: { topic: 'x' }, idempotency_key: `e2e-branch-${Date.now()}` },
      });
      expect([200, 201, 202]).toContain(start.status());
      const run = await start.json() as { run_id: string };

      await expect.poll(async () => {
        const detail = await api.get(`/workflow-runs/${run.run_id}`, { headers: { Authorization: `Bearer ${accessToken}` } });
        return (await detail.json() as { run: { status: string } }).run.status;
      }, { timeout: 20000 }).toBe('paused');

      const detail = await (await api.get(`/workflow-runs/${run.run_id}`, { headers: { Authorization: `Bearer ${accessToken}` } })).json() as {
        node_attempts: Array<{ node_id: string; status: string; selected_edges?: string[] }>;
        approvals: Array<{ node_id: string; status: string }>;
      };
      const conditionAttempt = detail.node_attempts.find((attempt) => attempt.node_id === 'node-condition');
      expect(conditionAttempt?.status).toBe('succeeded');
      // 命中「是」分支（edge-yes），未命中 default 边
      expect(conditionAttempt?.selected_edges).toContain('edge-yes');
      expect(conditionAttempt?.selected_edges).not.toContain('edge-default');
      expect(detail.approvals.some((approval) => approval.node_id === 'node-approval' && approval.status === 'pending')).toBe(true);
      expect(queryTenant(body.tenant_id,
        `SELECT status FROM workflow_runs WHERE id='${run.run_id}'`,
      )).toContain('paused');
    } finally {
      await api.dispose();
    }
  });

  test('condition 节点存在两条 default 边时校验失败并提示', async ({ context, page }) => {
    test.skip(page.viewportSize()!.width < 768, 'the visual designer is intentionally desktop-only');
    await createRealSession(context, 'admin');
    await page.goto('/workflows/new');
    await page.getByRole('button', { name: '添加条件判断节点' }).click();
    await page.getByRole('button', { name: '添加Agent节点' }).click();
    await page.getByRole('button', { name: '添加人工审批节点' }).click();
    const condition = page.locator('.react-flow__node').filter({ hasText: '条件判断' }).first();
    const agent = page.locator('.react-flow__node').filter({ hasText: 'Agent' }).first();
    const approval = page.locator('.react-flow__node').filter({ hasText: '人工审批' }).first();
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="yes"]'), agent.locator('.react-flow__handle.target'));
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="no"]'), approval.locator('.react-flow__handle.target'));
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="default"]'), agent.locator('.react-flow__handle.target'));
    await connectHandles(page, condition.locator('.workflow-handle-branch[data-handleid="default"]'), approval.locator('.react-flow__handle.target'));
    await expect(page.locator('.react-flow__edge')).toHaveCount(4);

    await waitForDraft(page);
    const response = page.waitForResponse((r) => r.url().endsWith('/validate'), { timeout: 15000 });
    await page.getByRole('button', { name: '校验工作流' }).click();
    await expect(page.getByText(/条件.*default|default.*条件|恰好.*default|exactly one default/i)).toBeVisible();
    expect((await response).status()).toBe(400);
  });
});
