import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import { ResourceChangeProposalCard } from '../ResourceChangeProposalCard';

const Location = () => <span data-testid="location">{useLocation().pathname}</span>;

describe('ResourceChangeProposalCard', () => {
  it('shows a review summary and navigates to the governed review page', () => {
    render(<MemoryRouter><ResourceChangeProposalCard proposal={{
      id: 'proposal-1', resourceKind: 'agent', operation: 'create', status: 'ready_for_review',
      summary: 'create agent', expiresAt: '2026-07-28T00:00:00Z',
    }} /><Location /></MemoryRouter>);

    expect(screen.getByText('Agent')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('link', { name: '审阅变更' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/resource-change-proposals/proposal-1');
  });
});
