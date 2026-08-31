import { render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';

import { ModelListPage } from '../ModelListPage';

import { PlatformAdminGate } from '@/modules/iam';

const authState = vi.hoisted(() => ({ user: { global_role: 'user' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
vi.mock('@/modules/llm/hooks/useModels', () => ({
  useModels: () => ({
    models: [
      { id: 'm1', name: 'test-model', displayName: '测试模型', providerId: 'p1', capabilities: ['chat'], enabled: true },
    ],
    loading: false,
    refresh: vi.fn(),
    toggleModel: vi.fn(),
    updateModel: vi.fn(),
    updateModelPolicy: vi.fn(),
    deleteModel: vi.fn(),
  }),
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'user' };
});

it('disables edit/delete and the enable toggle for a plain member', async () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <ModelListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试模型')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '编辑' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /删除/ })).toBeDisabled();
  // 启停 Switch 置灰（antd v5 用 button[role="switch"].ant-switch-disabled 判定）
  const switches = document.querySelectorAll('button[role="switch"].ant-switch-disabled');
  expect(switches.length).toBe(1);
  expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled();
});

it('enables write controls for a system_admin', async () => {
  authState.user = { global_role: 'system_admin' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <ModelListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试模型')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '编辑' })).not.toBeDisabled();
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
});
