import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { EvaluationCenterPage } from './EvaluationCenterPage';

const center = vi.hoisted(() => ({
  overview: { resources: 1, suites: 2, runs: 3, candidates: 1, experiments: 1 },
  resources: { items: [
    { id: 'r1', resource_id: 'skill-1', resource_kind: 'skill', status: 'active', stable_revision_id: 'v1',
      latest_run_status: 'succeeded', safe_summary: { name: '客服技能' }, created_at: '2026-07-23T00:00:00Z' },
    { id: 'r2', resource_id: 'agent-1', resource_kind: 'agent', status: 'active', stable_revision_id: 'agent-v1',
      latest_run_status: 'succeeded', safe_summary: { name: '客服 Agent' }, created_at: '2026-07-23T00:00:00Z' },
    { id: 'r3', resource_id: 'mcp-1', resource_kind: 'mcp', status: 'active', stable_revision_id: 'mcp-v1',
      safe_summary: { name: '检索 MCP' }, created_at: '2026-07-23T00:00:00Z' },
    { id: 'r4', resource_id: 'knowledge-1', resource_kind: 'knowledge', status: 'active', stable_revision_id: 'knowledge-v1',
      safe_summary: { name: '产品知识库' }, created_at: '2026-07-23T00:00:00Z' },
  ] },
  suites: { items: [] }, runs: { items: [] }, candidates: { items: [] }, experiments: { items: [{
    id: 'experiment-1', resource_id: 'agent-1', stable_revision_id: 'stable-1', canary_revision_id: 'canary-1',
    status: 'running', recommendation: 'promote', resource_kind: 'agent', stage_percent: 100, safety_stopped: false,
    state_version: 2, promotion_evidence: { eligible: true, gates: { quality: 'passed', cost: 'passed',
      latency: 'passed', error_rate: 'passed', security: 'passed' }, blockers: [] }, created_at: '2026-07-23T00:00:00Z',
  }] },
  loading: false, error: '', canManageEvaluation: true, reload: vi.fn(), rejectCandidate: vi.fn(),
  pauseExperiment: vi.fn(), promoteExperiment: vi.fn(), rollbackExperiment: vi.fn(), createEvaluation: vi.fn(),
}));
const useCenter = vi.hoisted(() => vi.fn(() => center));
vi.mock('../hooks/useEvaluationCenter', () => ({ useEvaluationCenter: useCenter }));

const LocationProbe = () => {
  const location = useLocation();
  const navigate = useNavigate();
  return <>
    <output aria-label="当前查询参数">{location.search}</output>
    <button type="button" onClick={() => navigate(-1)}>返回</button>
  </>;
};

const renderPage = (entry = '/evaluations') => {
  render(
    <MemoryRouter
      initialEntries={[entry]}
    >
      <EvaluationCenterPage />
      <LocationProbe />
    </MemoryRouter>,
  );
};

describe('EvaluationCenterPage', () => {
  beforeEach(() => { center.canManageEvaluation = true; useCenter.mockClear(); });

  it('exposes only the three primary first-viewport decisions', () => {
    renderPage();
    expect(screen.getByRole('combobox', { name: '资源类型' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '资源状态' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /新建评测/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '刷新' })).not.toBeInTheDocument();
  });

  it('keeps new evaluation hidden for members while details remain available', () => {
    center.canManageEvaluation = false;
    renderPage();
    expect(screen.queryByRole('button', { name: /新建评测/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查看 skill-1' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看 skill-1' }));
    expect(screen.getByText('观测事实')).toBeInTheDocument();
  });

  it('creates a suite and baseline run then refreshes through the center hook', async () => {
    center.createEvaluation.mockResolvedValue({ job_id: 'job-1', status: 'queued' });
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: /新建评测/ }));
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '目标资源' }));
    expect(await screen.findByText('检索 MCP（mcp-1）')).toBeInTheDocument();
    expect(screen.getByText('产品知识库（knowledge-1）')).toBeInTheDocument();
    fireEvent.click(await screen.findByText('客服 Agent（agent-1）'));
    fireEvent.change(screen.getByLabelText('评测名称'), { target: { value: '客服基线评测' } });
    fireEvent.change(screen.getByLabelText('用例名称'), { target: { value: '标准问候' } });
    fireEvent.change(screen.getByLabelText('测试输入'), { target: { value: '你好' } });
    fireEvent.change(screen.getByLabelText('期望输出'), { target: { value: '您好' } });
    fireEvent.click(screen.getByRole('button', { name: '创建并运行' }));
    await waitFor(() => expect(center.createEvaluation).toHaveBeenCalledWith(expect.objectContaining({
      resource: expect.objectContaining({ kind: 'agent', resource_id: 'agent-1', revision_id: 'agent-v1' }),
      name: '客服基线评测',
    })));
  });

  it('enables promotion from the real eligible experiment summary shape', () => {
    renderPage();
    fireEvent.click(screen.getByRole('tab', { name: '金丝雀实验 1' }));
    fireEvent.click(screen.getByRole('button', { name: '详情' }));
    expect(screen.getByRole('button', { name: /晋\s*级/ })).toBeEnabled();
  });

  it('initializes the center from a valid resource deep link', () => {
    renderPage('/evaluations?kind=skill&resource_id=skill-1');
    expect(useCenter).toHaveBeenLastCalledWith({ resource_kind: 'skill', resource_id: 'skill-1', status: undefined });
  });

  it('ignores an unsupported resource kind without dropping the resource id', () => {
    renderPage('/evaluations?kind=workflow&resource_id=resource-1');
    expect(useCenter).toHaveBeenLastCalledWith({ resource_kind: undefined, resource_id: 'resource-1', status: undefined });
  });

  it('keeps the resource deep link while changing kind and follows history navigation', async () => {
    renderPage('/evaluations?kind=skill&resource_id=skill-1&view=evidence');
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '资源类型' }));
    const option = await waitFor(() => {
      const item = Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option-content'))
        .find((value) => value.textContent === 'Agent');
      expect(item).toBeDefined();
      return item!;
    });
    fireEvent.click(option);
    await waitFor(() => expect(screen.getByRole('status', { name: '当前查询参数' }))
      .toHaveTextContent('?kind=agent&resource_id=skill-1&view=evidence'));
    expect(useCenter).toHaveBeenLastCalledWith({ resource_kind: 'agent', resource_id: 'skill-1', status: undefined });

    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    await waitFor(() => expect(useCenter).toHaveBeenLastCalledWith({
      resource_kind: 'skill', resource_id: 'skill-1', status: undefined,
    }));
  });
});
