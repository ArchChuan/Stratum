import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { RuntimeHealthTrendPanel } from './RuntimeHealthTrendPanel';

import { EVALUATION_TREND_RUN_LIMIT } from '@/constants';

const mocks = vi.hoisted(() => ({
  listResources: vi.fn(),
  listRuns: vi.fn(),
}));
vi.mock('../api/evaluation.api', () => ({
  evaluationApi: { listResources: mocks.listResources, listRuns: mocks.listRuns },
}));

const agentResource = { id: 'agent-1', resource_id: 'agent-1', resource_kind: 'agent',
  status: 'active', created_at: '2026-08-01T00:00:00Z' };
const skillResource = { id: 'skill-1', resource_id: 'skill-1', resource_kind: 'skill',
  status: 'active', created_at: '2026-08-01T00:00:00Z' };

const run = (id: string, createdAt: string, passed: boolean, passedCases: number, totalCases: number,
  revision: string, resourceId = 'agent-1', resourceKind: string = 'agent') => ({
  id, resource_id: resourceId, revision_id: revision, status: 'succeeded', resource_kind: resourceKind,
  passed, total_cases: totalCases, passed_cases: passedCases, created_at: createdAt,
});

describe('RuntimeHealthTrendPanel', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listResources.mockResolvedValue({ items: [agentResource] });
    mocks.listRuns.mockResolvedValue({ items: [] });
  });

  it('loads runs for the default resource and renders the pass-rate trend summary', async () => {
    mocks.listRuns.mockResolvedValue({ items: [
      run('run-1', '2026-08-01T02:00:00Z', false, 6, 10, 'rev-a'),
      run('run-2', '2026-08-05T02:00:00Z', true, 8, 10, 'rev-b'),
      run('run-3', '2026-08-09T02:00:00Z', true, 9, 10, 'rev-b'),
    ] });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);

    expect(await screen.findByText(/本窗口\s*3\s*次/)).toBeInTheDocument();
    expect(screen.getByText(/平均通过率\s*76\.7%/)).toBeInTheDocument();
    expect(screen.getByTestId('health-trend-chart')).toBeInTheDocument();
    expect(mocks.listRuns).toHaveBeenCalledWith({ resource_kind: 'agent', resource_id: 'agent-1',
      limit: EVALUATION_TREND_RUN_LIMIT });

    const table = await screen.findByRole('table');
    expect(within(table).getByText('run-1')).toBeInTheDocument();
    expect(within(table).getByText('90.0%')).toBeInTheDocument();
  });

  it('annotates truncation when the backend reports earlier runs via next_cursor', async () => {
    mocks.listRuns.mockResolvedValue({ items: [
      run('run-1', '2026-08-01T02:00:00Z', true, 10, 10, 'rev-a'),
    ], next_cursor: 'cursor-1' });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);

    expect(await screen.findByText(/本窗口\s*1\s*次/)).toBeInTheDocument();
    expect(screen.getByText(`仅显示最近 ${EVALUATION_TREND_RUN_LIMIT} 次运行（存在更早记录）`)).toBeInTheDocument();
  });

  it('renders an empty hint when the resource has no run records', async () => {
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);

    expect(await screen.findByText('该资源尚无评测运行记录')).toBeInTheDocument();
    expect(mocks.listRuns).toHaveBeenCalledWith({ resource_kind: 'agent', resource_id: 'agent-1',
      limit: EVALUATION_TREND_RUN_LIMIT });
  });

  it('re-calls listResources with the new kind and drops stale chart/table', async () => {
    mocks.listRuns.mockResolvedValue({ items: [run('run-1', '2026-08-01T02:00:00Z', true, 9, 10, 'rev-a')] });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);
    expect(await screen.findByText(/本窗口\s*1\s*次/)).toBeInTheDocument();

    // 切换到技能类型：资源候选需按新 kind 拉取，已选资源与旧 runs 清空。
    mocks.listResources.mockResolvedValue({ items: [skillResource] });
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '资源类型' }));
    fireEvent.click(await screen.findByText('技能'));

    await waitFor(() => expect(mocks.listResources).toHaveBeenCalledWith({ resource_kind: 'skill',
      limit: EVALUATION_TREND_RUN_LIMIT }));
    expect(screen.queryByText(/本窗口/)).toBeNull();
    expect(screen.queryByText('run-1')).toBeNull();
    expect(screen.queryByTestId('health-trend-chart')).toBeNull();
    expect(screen.getByText('选择资源类型与资源后展示其评测运行通过率趋势')).toBeInTheDocument();
  });

  it('clears stale runs and renders an error-retry state when the next load fails', async () => {
    // 首次 agent-1 加载成功；同 kind 下切到 agent-2 后 runs 加载失败；点「重试」恢复。
    const agent2Resource = { ...agentResource, id: 'agent-2', resource_id: 'agent-2' };
    mocks.listResources.mockResolvedValue({ items: [agentResource, agent2Resource] });
    mocks.listRuns
      .mockResolvedValueOnce({ items: [run('run-1', '2026-08-01T02:00:00Z', true, 9, 10, 'rev-a')] })
      .mockRejectedValueOnce(new Error('mock: 运行记录服务不可用'))
      .mockResolvedValue({ items: [run('run-2', '2026-08-02T02:00:00Z', true, 8, 10, 'rev-b',
        'agent-2')] });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);
    expect(await screen.findByText(/本窗口\s*1\s*次/)).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '资源' }));
    const agent2Option = await waitFor(() => {
      const item = Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option-content'))
        .find((value) => value.textContent === 'agent-2');
      expect(item).toBeDefined();
      return item!;
    });
    fireEvent.click(agent2Option);
    await waitFor(() => expect(mocks.listRuns).toHaveBeenCalledTimes(2));

    expect(await screen.findByText('mock: 运行记录服务不可用')).toBeInTheDocument();
    expect(screen.queryByText('该资源尚无评测运行记录')).toBeNull();
    expect(screen.queryByText('run-1')).toBeNull();
    expect(screen.queryByTestId('health-trend-chart')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    expect(await screen.findByText(/本窗口\s*1\s*次/)).toBeInTheDocument();
    expect(screen.queryByText('mock: 运行记录服务不可用')).toBeNull();
    expect(screen.getByTestId('health-trend-chart')).toBeInTheDocument();
  });
});
