import { fireEvent, render, screen } from '@testing-library/react';
import { Modal } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { ExperimentDrawer } from './ExperimentDrawer';
import { promotionBlockReason } from './experimentDecisions';

const base = {
  id: 'experiment-1', resource_id: 'agent-1', stable_revision_id: 'stable-1', canary_revision_id: 'canary-1',
  status: 'running', recommendation: 'promote', resource_kind: 'agent' as const, stage_percent: 100,
  safety_stopped: false, state_version: 2, created_at: '2026-07-23T00:00:00Z',
  gates: { quality: 'passed' as const, cost: 'passed' as const, latency: 'passed' as const,
    error_rate: 'passed' as const, security: 'passed' as const },
  promotion_evidence: { eligible: true, gates: { quality: 'passed' as const, cost: 'passed' as const,
    latency: 'passed' as const, error_rate: 'passed' as const, security: 'passed' as const }, blockers: [] },
};

const blocked = (code: 'insufficient_samples' | 'insufficient_duration' | 'evidence_unavailable'
  | 'guardrail_violation' | 'safety_stop' | 'recommendation_hold', message: string) => ({
  ...base, promotion_evidence: { ...base.promotion_evidence, eligible: false,
    blockers: [{ code, category: code, message }] },
});

describe('promotionBlockReason', () => {
  it.each([
    [blocked('insufficient_samples', '样本量不足'), '样本量不足'],
    [blocked('insufficient_duration', '观测时长不足'), '观测时长不足'],
    [blocked('evidence_unavailable', '证据依赖暂不可用'), '证据依赖暂不可用'],
    [blocked('guardrail_violation', '成本门禁违反'), '成本门禁违反'],
    [blocked('safety_stop', '安全停止已触发'), '安全停止已触发'],
    [blocked('recommendation_hold', '系统建议继续观察'), '系统建议继续观察'],
  ])('distinguishes promotion blocker %#', (experiment, reason) => {
    expect(promotionBlockReason(experiment)).toContain(reason);
  });
});

describe('ExperimentDrawer', () => {
  it('hides all decision commands from members', () => {
    render(<ExperimentDrawer experiment={base} open onClose={vi.fn()} canManage={false}
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(screen.getByText('观测事实')).toBeInTheDocument();
    expect(screen.queryByText('管理员决定')).not.toBeInTheDocument();
  });

  it.each([
    ['暂停实验', '确认暂停金丝雀实验？', '暂停后'],
    ['晋级', '确认晋级此实验？', '扩大候选版本流量'],
    ['回滚', '确认回滚此实验？', '切回稳定版本'],
  ])('confirms %s with consequence text', (button, title, consequence) => {
    const confirm = vi.spyOn(Modal, 'confirm').mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() } as never));
    render(<ExperimentDrawer experiment={base} open onClose={vi.fn()} canManage
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: new RegExp(button.split('').join('\\s*')) }));
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ title, content: expect.stringContaining(consequence) }));
  });

  it('enforces running paused and terminal command matrices in fixed drawer actions', () => {
    const { rerender } = render(<ExperimentDrawer experiment={base} open onClose={vi.fn()} canManage
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(screen.getByRole('button', { name: '暂停实验' })).toBeInTheDocument();
    expect(document.querySelector('.ant-drawer-footer')).toBeInTheDocument();
    rerender(<ExperimentDrawer experiment={{ ...base, status: 'paused' }} open onClose={vi.fn()} canManage
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(screen.queryByRole('button', { name: '暂停实验' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /回\s*滚/ })).toBeInTheDocument();
    rerender(<ExperimentDrawer experiment={{ ...base, status: 'completed' }} open onClose={vi.fn()} canManage
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(document.querySelector('.ant-drawer-footer')).not.toBeInTheDocument();
  });
});
