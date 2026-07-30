import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AgentManagementPage } from '../AgentManagementPage';

vi.mock('../PlatformAssistantPage', () => ({
  PlatformAssistantPage: () => <div>平台助手会话</div>,
}));
vi.mock('../AgentsListPage', () => ({
  AgentsListPage: () => <div>普通 Agent 内容</div>,
}));

const LocationProbe = () => <span data-testid="location">{useLocation().pathname}</span>;

describe('AgentManagementPage', () => {
  it('defaults to the platform assistant and keeps the list in a route-backed tab', () => {
    render(
      <MemoryRouter initialEntries={['/agents']}>
        <AgentManagementPage />
        <LocationProbe />
      </MemoryRouter>,
    );

    expect(screen.getByRole('tab', { name: '平台助手' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('平台助手会话')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Agent 列表' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/agents/list');
  });
});
