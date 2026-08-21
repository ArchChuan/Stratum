import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { SettingsPage } from './SettingsPage';

const useTenantSettingsMock = vi.hoisted(() => vi.fn());

vi.mock('../../hooks/useTenantSettings', () => ({
  useTenantSettings: useTenantSettingsMock,
}));

// 迁移卡片自身有网络依赖（迁移记录 + 模型目录 + 租户 settings），在页面级测试中

describe('SettingsPage tenant deletion visibility', () => {
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  });

  const renderPage = (isDefault: boolean | null) => {
    useTenantSettingsMock.mockReturnValue({
      user: { current_tenant: { id: 'tenant-1', name: '租户一', role: 'owner' } },
      role: 'owner',
      loading: isDefault === null,
      tenantName: '租户一',
      isDefault,
      handleBasicSave: vi.fn(),
    });
    return render(<MemoryRouter><SettingsPage /></MemoryRouter>);
  };

  it('does not flash tenant deletion while tenant type is unknown', () => {
    renderPage(null);

    expect(screen.queryByRole('button', { name: '删除租户' })).not.toBeInTheDocument();
    expect(screen.queryByText('危险操作')).not.toBeInTheDocument();
  });

  it('shows tenant deletion only after a non-default tenant is confirmed', () => {
    renderPage(false);

    expect(screen.getByRole('button', { name: '删除租户' })).toBeInTheDocument();
  });

  it('keeps tenant deletion hidden for the default tenant', () => {
    renderPage(true);

    expect(screen.queryByRole('button', { name: '删除租户' })).not.toBeInTheDocument();
  });
});
