import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, vi } from 'vitest';

import { SkillEvaluationPanel } from './SkillEvaluationPanel';

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), createBaseline: vi.fn(), error: vi.fn() }));
vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
  Link: ({ to, children, ...props }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) => (
    <a href={to} {...props}>{children}</a>
  ),
}));
vi.mock('../api/evaluation.api', () => ({ evaluationApi: { createBaseline: mocks.createBaseline } }));
vi.mock('antd', async () => ({ ...(await vi.importActual<typeof import('antd')>('antd')),
  message: { error: mocks.error },
}));

describe('SkillEvaluationPanel', () => {
  beforeEach(() => { Object.values(mocks).forEach((mock) => mock.mockReset()); });

  it('keeps the center as the read-only entry for members', () => {
    render(<SkillEvaluationPanel skillId="skill with space" stableRevisionId="stable-1" isAdmin={false} />);

    expect(screen.getByRole('link', { name: '打开评测与进化中心' })).toHaveAttribute(
      'href', '/evaluations?kind=skill&resource_id=skill%20with%20space',
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('registers the first published baseline before an administrator opens the center', async () => {
    mocks.createBaseline.mockResolvedValue({ kind: 'skill', resource_id: 'skill with space', revision_id: 'stable-1' });
    render(<SkillEvaluationPanel skillId="skill with space" stableRevisionId="stable-1" isAdmin />);

    fireEvent.click(screen.getByRole('button', { name: '建立评测基线并打开中心' }));

    await waitFor(() => expect(mocks.createBaseline).toHaveBeenCalledWith('skill', 'skill with space'));
    expect(mocks.navigate).toHaveBeenCalledWith('/evaluations?kind=skill&resource_id=skill%20with%20space');
  });

  it('keeps the published revision warning before publish', () => {
    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="" isAdmin />);
    expect(screen.getByText('请先发布 Skill，再进行评测与优化。')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '打开评测与进化中心' })).not.toBeInTheDocument();
  });
});
