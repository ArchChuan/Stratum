import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Modal } from 'antd';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { UseMemoryMigrationResult } from '../../hooks/useMemoryMigration';
import { MemoryMigrationCard } from '../MemoryMigrationCard';


const useMemoryMigrationMock = vi.hoisted(() => vi.fn());
vi.mock('../../hooks/useMemoryMigration', () => ({
  useMemoryMigration: useMemoryMigrationMock,
}));

const migratingRecord = {
  id: 7,
  from_model: 'text-embedding-v1',
  to_model: 'text-embedding-v3',
  status: 'migrating',
  progress: 30,
  total_facts: 100,
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:00:00Z',
};

const baseState = {
  migration: null,
  loading: false,
  currentModel: 'text-embedding-v1',
  models: [
    {
      provider: 'OpenAI',
      models: [{ value: 'text-embedding-v3', label: 'v3', health: 'healthy' }],
    },
  ],
  modelsLoading: false,
  targetModel: undefined,
  setTargetModel: vi.fn(),
  cost: null,
  costLoading: false,
  starting: false,
  canceling: false,
  retrying: false,
  fetchCost: vi.fn(),
  startMigration: vi.fn(),
  cancelMigration: vi.fn(),
  retryMigration: vi.fn(),
};

const mockHook = (overrides: Partial<UseMemoryMigrationResult> = {}) => {
  useMemoryMigrationMock.mockReturnValue({ ...baseState, ...overrides });
};

describe('MemoryMigrationCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  });

  it('renders current model tag and disables start without a target', () => {
    mockHook({ currentModel: 'text-embedding-v1' });

    render(<MemoryMigrationCard />);

    expect(screen.getByText('记忆嵌入模型')).toBeInTheDocument();
    expect(screen.getByText('text-embedding-v1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /开始迁移/ })).toBeDisabled();
  });

  it('shows unconfigured hint when no effective model exists', () => {
    mockHook({ currentModel: '' });

    render(<MemoryMigrationCard />);

    expect(screen.getByText(/未配置/)).toBeInTheDocument();
  });

  it('disables start when target equals the current model', () => {
    mockHook({ currentModel: 'text-embedding-v1', targetModel: 'text-embedding-v1' });

    render(<MemoryMigrationCard />);

    expect(screen.getByRole('button', { name: /开始迁移/ })).toBeDisabled();
  });

  it('locks the target selector and shows progress while migrating', () => {
    mockHook({ migration: migratingRecord, targetModel: 'text-embedding-v3' });

    render(<MemoryMigrationCard />);

    expect(document.querySelector('.ant-select-disabled')).not.toBeNull();
    expect(screen.getByRole('button', { name: /开始迁移/ })).toBeDisabled();
    expect(screen.getByText('30 / 100')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '取消迁移' })).toBeInTheDocument();
  });

  it('cancels via the migrating action', () => {
    const cancelMigration = vi.fn();
    mockHook({ migration: migratingRecord, cancelMigration });

    render(<MemoryMigrationCard />);

    fireEvent.click(screen.getByRole('button', { name: '取消迁移' }));
    expect(cancelMigration).toHaveBeenCalledWith(7);
  });

  it('shows the done hint after completion', () => {
    mockHook({ migration: { ...migratingRecord, status: 'done', progress: 100 } });

    render(<MemoryMigrationCard />);

    expect(screen.getByText(/迁移已完成/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /重\s*试/ })).not.toBeInTheDocument();
  });

  it('offers retry on failure', () => {
    const retryMigration = vi.fn();
    mockHook({ migration: { ...migratingRecord, status: 'failed' }, retryMigration });

    render(<MemoryMigrationCard />);

    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    expect(retryMigration).toHaveBeenCalledWith(7);
  });

  it('offers retry after cancellation', () => {
    mockHook({ migration: { ...migratingRecord, status: 'canceled' } });

    render(<MemoryMigrationCard />);

    expect(screen.getByText(/迁移已取消/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /重\s*试/ })).toBeInTheDocument();
  });

  it('shows cost preview and confirms before starting migration', async () => {
    const confirmSpy = vi.spyOn(Modal, 'confirm').mockImplementation((() => {}) as never);
    const startMigration = vi.fn();
    const fetchCost = vi.fn().mockResolvedValue({ fact_count: 120, estimated_seconds: 24 });
    mockHook({
      currentModel: 'text-embedding-v1',
      targetModel: 'text-embedding-v3',
      fetchCost,
      startMigration,
    });

    render(<MemoryMigrationCard />);

    fireEvent.click(screen.getByRole('button', { name: /开始迁移/ }));

    await waitFor(() => expect(confirmSpy).toHaveBeenCalled());
    const confirmArgs = confirmSpy.mock.calls[0][0];
    expect(confirmArgs.title).toBe('确认切换嵌入模型？');

    // 确认弹窗内展示迁移成本（存量条数 + 预计时长）
    render(<div>{confirmArgs.content as ReactElement}</div>);
    expect(screen.getByText(/共 120 条已提取事实/)).toBeInTheDocument();
    expect(screen.getByText(/约 24 秒/)).toBeInTheDocument();

    await confirmArgs.onOk!();
    expect(startMigration).toHaveBeenCalledWith('text-embedding-v3');
  });
});
