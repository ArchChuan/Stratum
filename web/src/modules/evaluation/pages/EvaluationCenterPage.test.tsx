import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
  suites: { items: [] }, runs: { items: [] }, candidates: { items: [] }, experiments: { items: [] },
  loading: false, error: '', canManageEvaluation: true, reload: vi.fn(), rejectCandidate: vi.fn(),
  pauseExperiment: vi.fn(), promoteExperiment: vi.fn(), rollbackExperiment: vi.fn(), createEvaluation: vi.fn(),
}));
vi.mock('../hooks/useEvaluationCenter', () => ({ useEvaluationCenter: () => center }));

describe('EvaluationCenterPage', () => {
  beforeEach(() => { center.canManageEvaluation = true; });

  it('exposes only the three primary first-viewport decisions', () => {
    render(<EvaluationCenterPage />);
    expect(screen.getByRole('combobox', { name: '资源类型' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '资源状态' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /新建评测/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '刷新' })).not.toBeInTheDocument();
  });

  it('keeps new evaluation hidden for members while details remain available', () => {
    center.canManageEvaluation = false;
    render(<EvaluationCenterPage />);
    expect(screen.queryByRole('button', { name: /新建评测/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查看 skill-1' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看 skill-1' }));
    expect(screen.getByText('观测事实')).toBeInTheDocument();
  });

  it('creates a suite and baseline run then refreshes through the center hook', async () => {
    center.createEvaluation.mockResolvedValue({ job_id: 'job-1', status: 'queued' });
    render(<EvaluationCenterPage />);
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
});
