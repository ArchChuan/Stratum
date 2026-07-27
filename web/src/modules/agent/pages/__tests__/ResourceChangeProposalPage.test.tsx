import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ResourceChangeProposalPage } from '../ResourceChangeProposalPage';

const confirm = vi.fn();
const cancel = vi.fn();

vi.mock('../../hooks/useResourceChangeProposal', () => ({
  useResourceChangeProposal: () => ({
    proposal: {
      id: 'proposal-1', proposerId: 'admin-1', resourceKind: 'knowledge_workspace', operation: 'update',
      resourceId: 'workspace-1', baselineFingerprint: 'sha256:baseline',
      baselineProjection: { name: '官方文档', description: '旧说明', embeddingModel: 'text-embedding-v2' },
      payload: { name: '官方文档', description: '已核验资料', embeddingModel: 'text-embedding-v3' },
      summary: 'create knowledge workspace', status: 'ready_for_review', events: [],
      expiresAt: '2026-07-28T00:00:00Z', createdAt: '2026-07-27T00:00:00Z', updatedAt: '2026-07-27T00:00:00Z',
    },
    loading: false, saving: false, confirming: false, canceling: false,
    saveDraft: vi.fn(), confirm, cancel,
  }),
}));

describe('ResourceChangeProposalPage', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows field-level changes and requires an explicit confirmation', async () => {
    render(<MemoryRouter initialEntries={['/resource-change-proposals/proposal-1']}><Routes>
      <Route path="/resource-change-proposals/:id" element={<ResourceChangeProposalPage />} />
    </Routes></MemoryRouter>);

    expect(screen.getAllByText('官方文档')).toHaveLength(2);
    expect(screen.getAllByText('已核验资料')).not.toHaveLength(0);
    expect(screen.getByText('旧说明')).toBeInTheDocument();
    expect(screen.getByText('已记录，确认时重新校验')).toBeInTheDocument();
    expect(screen.getByText('租户管理员或所有者')).toBeInTheDocument();
    expect(screen.queryByText(/token|apiKey|headers|env/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认并应用' }));
    expect(await screen.findAllByText('确认应用这次变更？')).not.toHaveLength(0);
  });

  it('does not offer a retry command for unknown outcomes', () => {
    render(<MemoryRouter initialEntries={['/resource-change-proposals/proposal-1']}><Routes>
      <Route path="/resource-change-proposals/:id" element={<ResourceChangeProposalPage />} />
    </Routes></MemoryRouter>);
    expect(screen.queryByRole('button', { name: /重试/ })).not.toBeInTheDocument();
  });
});
