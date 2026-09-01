import { render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';

import { ProviderListPage } from '../ProviderListPage';

import { PlatformAdminGate } from '@/modules/iam';

const authState = vi.hoisted(() => ({ user: { global_role: 'user' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
vi.mock('@/modules/llm/hooks/useProviders', () => ({
  useProviders: () => ({
    providers: [
      { id: 'p1', name: '测试厂商', kind: 'openai_compat', baseUrl: 'https://example.com', defaultModel: '', enabled: true },
    ],
    loading: false,
    createLoading: false,
    updateLoading: false,
    refresh: vi.fn(),
    createProvider: vi.fn(),
    updateProvider: vi.fn(),
    deleteProvider: vi.fn(),
  }),
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'user' };
});

it('disables every write control for a plain member and keeps 刷新 enabled', async () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <ProviderListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试厂商')).toBeInTheDocument();
  // 只读提示条
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  // 写控件全部置灰
  expect(screen.getByRole('button', { name: '添加厂商' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '添加模型' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '发现模型' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '健康检查' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /编辑/ })).toBeDisabled();
  expect(screen.getByRole('button', { name: /删除/ })).toBeDisabled();
  // 读操作刷新保持可用
  expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled();
});

it('enables write controls for a system_admin', async () => {
  authState.user = { global_role: 'system_admin' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <ProviderListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试厂商')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '添加厂商' })).not.toBeDisabled();
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
});
