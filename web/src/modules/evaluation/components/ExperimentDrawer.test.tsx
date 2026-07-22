import { fireEvent, render, screen } from '@testing-library/react';
import { Modal } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { ExperimentDrawer } from './ExperimentDrawer';

const experiment = {
  id: 'experiment-1', resource_id: 'agent-1', stable_revision_id: 'stable-1', canary_revision_id: 'canary-1',
  status: 'active', recommendation: '继续观察', resource_kind: 'agent' as const, stage_percent: 5,
  safety_stopped: false, state_version: 2, created_at: '2026-07-23T00:00:00Z',
  gates: { quality: 'passed' as const, cost: 'pending' as const, latency: 'passed' as const,
    error_rate: 'failed' as const, security: 'passed' as const },
};

describe('ExperimentDrawer', () => {
  it('hides all decision commands from members', () => {
    render(<ExperimentDrawer experiment={experiment} open onClose={vi.fn()} canManage={false}
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(screen.getByText('观测事实')).toBeInTheDocument();
    expect(screen.queryByText('管理员决定')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '暂停实验' })).not.toBeInTheDocument();
  });

  it('explains failed gates and confirms destructive decisions', () => {
    const confirm = vi.spyOn(Modal, 'confirm').mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() } as never));
    render(<ExperimentDrawer experiment={experiment} open onClose={vi.fn()} canManage
      onPause={vi.fn()} onPromote={vi.fn()} onRollback={vi.fn()} />);
    expect(screen.getByRole('button', { name: /晋\s*级/ })).toBeDisabled();
    expect(screen.getAllByText(/错误率门禁未通过/).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: '暂停实验' }));
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '确认暂停金丝雀实验？' }));
  });
});
