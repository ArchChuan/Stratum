import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AgentManagementPage } from '../AgentManagementPage';

vi.mock('../AgentsListPage', () => ({
  AgentsListPage: () => <div>Agent 列表内容</div>,
}));

describe('AgentManagementPage', () => {
  it('renders agent list directly at /agents', () => {
    render(
      <MemoryRouter initialEntries={['/agents']}>
        <AgentManagementPage />
      </MemoryRouter>,
    );

    expect(screen.getByText('Agent 列表内容')).toBeInTheDocument();
  });
});
