import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';

import { PlatformAdminGate, usePlatformAdminCanEdit } from '../PlatformAdminGate';

const authState = vi.hoisted(() => ({ user: { global_role: 'system_admin' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));

const Probe = () => <div data-testid="can-edit">{String(usePlatformAdminCanEdit())}</div>;

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'system_admin' };
});

it('renders a readonly alert and canEdit=false for a plain member', () => {
  authState.user = { global_role: 'user' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('false');
});

it('hides the alert and keeps canEdit=true for a system_admin', () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('true');
});

it('requires the minRole rank: a system_admin cannot edit a global_admin gate', () => {
  render(
    <PlatformAdminGate minRole="global_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('false');
});
