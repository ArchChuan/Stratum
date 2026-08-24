import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { PlatformAssistantPage } from '../PlatformAssistantPage';

vi.mock('../AgentChatPage', () => ({
  AgentChatPage: ({ fixedAgentId }: { fixedAgentId?: string }) => <div data-testid="chat">{fixedAgentId}</div>,
}));
vi.mock('@/modules/iam', () => ({
  useTenantRole: () => ({ isAdmin: true }),
  useAuth: () => ({ user: { sub: 'test-user', tenant_id: 't1', role: 'member' } }),
}));

describe('PlatformAssistantPage', () => {
  it('uses the fixed assistant without an agent selector and exposes settings to admins', () => {
    render(<MemoryRouter><PlatformAssistantPage /></MemoryRouter>);
    expect(screen.getByTestId('chat')).toHaveTextContent('stratum-platform-assistant');
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '平台助手设置' })).toHaveAttribute(
      'href', '/agents/stratum-platform-assistant/edit',
    );
  });
});
