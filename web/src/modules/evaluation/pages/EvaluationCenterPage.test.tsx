import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { EvaluationCenterPage } from './EvaluationCenterPage';

const center = vi.hoisted(() => ({
  overview: { resources: 1, suites: 2, runs: 3, candidates: 1, experiments: 1 },
  resources: { items: [{ id: 'r1', resource_id: 'skill-1', resource_kind: 'skill', status: 'active',
    stable_revision_id: 'v1', latest_run_status: 'succeeded', safe_summary: { name: '客服技能' }, created_at: '2026-07-23T00:00:00Z' }] },
  suites: { items: [] }, runs: { items: [] }, candidates: { items: [] }, experiments: { items: [] },
  loading: false, error: '', canManageEvaluation: true, reload: vi.fn(), rejectCandidate: vi.fn(),
  pauseExperiment: vi.fn(), promoteExperiment: vi.fn(), rollbackExperiment: vi.fn(),
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
});
