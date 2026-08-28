import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { MemorySnapshot } from '../../model/memory';
import { SnapshotPanel } from '../SnapshotPanel';

const { listSnapshotsMock } = vi.hoisted(() => ({ listSnapshotsMock: vi.fn() }));
vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: { listSnapshots: listSnapshotsMock, updateSnapshot: vi.fn(), deleteSnapshot: vi.fn() },
}));

const activeSnapshot: MemorySnapshot = {
  agent_id: 'agent-1',
  agent_name: '客服助手',
  conversation_name: '会话A',
  work_context: ['处理工单'],
  personal_context: ['喜欢简洁'],
  top_of_mind: ['跟进退款'],
  expires_at: '2099-01-01T00:00:00Z',
  updated_at: '2026-08-27T10:00:00Z',
  status: 'active',
};

const expiredSnapshot: MemorySnapshot = {
  ...activeSnapshot,
  agent_id: 'agent-2',
  agent_name: '通用助手',
  conversation_name: '',
  expires_at: '2020-01-01T00:00:00Z',
};

describe('SnapshotPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listSnapshotsMock.mockResolvedValue({ snapshots: [activeSnapshot, expiredSnapshot] });
  });

  it('titles each item with agent name plus conversation name, falling back to agent name alone', async () => {
    render(<SnapshotPanel />);
    await waitFor(() => expect(listSnapshotsMock).toHaveBeenCalledTimes(1));

    expect(screen.getByText('客服助手 · 会话A')).toBeTruthy();
    expect(screen.getByText('通用助手')).toBeTruthy();
  });

  it('greys out and tags expired snapshots', async () => {
    render(<SnapshotPanel />);
    await waitFor(() => expect(listSnapshotsMock).toHaveBeenCalledTimes(1));

    const expired = screen.getByText('已过期');
    expect(expired).toBeTruthy();
    // 只有过期的那条带「已过期」Tag。
    expect(screen.getAllByText('已过期')).toHaveLength(1);
  });

  it('opens a detail drawer showing the three context sections on 查看详情 click', async () => {
    render(<SnapshotPanel />);
    await waitFor(() => expect(listSnapshotsMock).toHaveBeenCalledTimes(1));

    const buttons = screen.getAllByText('查看详情');
    fireEvent.click(buttons[0]);

    await waitFor(() => expect(screen.getByText('工作上下文')).toBeTruthy());
    expect(screen.getByText('处理工单')).toBeTruthy();
    expect(screen.getByText('个人上下文')).toBeTruthy();
    expect(screen.getByText('喜欢简洁')).toBeTruthy();
    expect(screen.getByText('当前关注')).toBeTruthy();
    expect(screen.getByText('跟进退款')).toBeTruthy();
  });
});
