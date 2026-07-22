import { render, screen } from '@testing-library/react';
import { vi } from 'vitest';

import { SkillEvaluationPanel } from './SkillEvaluationPanel';

vi.mock('react-router-dom', () => ({
  Link: ({ to, children, ...props }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) => (
    <a href={to} {...props}>{children}</a>
  ),
}));

describe('SkillEvaluationPanel', () => {
  it.each([false, true])('uses the center as the single orchestration entry for admin=%s', (isAdmin) => {
    render(<SkillEvaluationPanel skillId="skill with space" stableRevisionId="stable-1" isAdmin={isAdmin} />);

    expect(screen.getByRole('link', { name: '打开评测与进化中心' })).toHaveAttribute(
      'href', '/evaluations?kind=skill&resource_id=skill%20with%20space',
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('keeps the published revision warning before publish', () => {
    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="" isAdmin />);
    expect(screen.getByText('请先发布 Skill，再进行评测与优化。')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '打开评测与进化中心' })).not.toBeInTheDocument();
  });
});
