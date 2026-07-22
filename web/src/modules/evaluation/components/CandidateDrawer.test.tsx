import { fireEvent, render, screen } from '@testing-library/react';
import { Modal } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { CandidateDrawer } from './CandidateDrawer';

const candidate = {
  id: 'candidate-1', resource_id: 'skill-1', revision_id: 'revision-2', parent_revision_id: 'revision-1',
  source: 'optimization', status: 'proposed', resource_kind: 'skill' as const, state_version: 1,
  safe_diff: { changed_fields: ['temperature'], changes: { temperature: { before: 0.2, after: 0.4 } }, parent_missing: false },
  created_at: '2026-07-23T00:00:00Z',
};

describe('CandidateDrawer', () => {
  it('renders only the safe field diff and uses a full-screen mobile drawer', () => {
    render(<CandidateDrawer candidate={candidate} open onClose={vi.fn()} canManage={false} isMobile onReject={vi.fn()} />);
    expect(screen.getByText('temperature')).toBeInTheDocument();
    expect(screen.getByText('0.2')).toBeInTheDocument();
    expect(document.querySelector('.ant-drawer-content-wrapper')).toHaveStyle({ width: '100%' });
  });

  it('shows reject only for proposed candidates and confirms its consequence', () => {
    const confirm = vi.spyOn(Modal, 'confirm').mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() } as never));
    const { rerender } = render(<CandidateDrawer candidate={candidate} open onClose={vi.fn()} canManage onReject={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '拒绝候选' }));
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '确认拒绝此候选版本？', content: expect.stringContaining('不会进入金丝雀实验') }));
    rerender(<CandidateDrawer candidate={{ ...candidate, status: 'rejected' }} open onClose={vi.fn()} canManage onReject={vi.fn()} />);
    expect(screen.queryByRole('button', { name: '拒绝候选' })).not.toBeInTheDocument();
  });
});
