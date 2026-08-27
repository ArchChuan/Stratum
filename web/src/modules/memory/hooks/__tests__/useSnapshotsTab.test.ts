import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useSnapshotsTab } from '../useSnapshotsTab';

const { listSnapshotsMock, updateSnapshotMock, deleteSnapshotMock } = vi.hoisted(() => ({
  listSnapshotsMock: vi.fn(),
  updateSnapshotMock: vi.fn(),
  deleteSnapshotMock: vi.fn(),
}));

vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: {
    listSnapshots: listSnapshotsMock,
    updateSnapshot: updateSnapshotMock,
    deleteSnapshot: deleteSnapshotMock,
  },
}));

const patchData = {
  work_context: ['当前项目'],
  personal_context: ['正在休假'],
  top_of_mind: ['快照保存失败时 Modal 保持打开'],
};

describe('useSnapshotsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    listSnapshotsMock.mockResolvedValue({ snapshots: [] });
  });

  it('loads snapshots on mount', async () => {
    const { result } = renderHook(() => useSnapshotsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listSnapshotsMock).toHaveBeenCalled();
    expect(result.current.snapshots).toHaveLength(0);
  });

  it('rethrows on updateSnapshot failure so the caller can keep the modal open', async () => {
    updateSnapshotMock.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useSnapshotsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await expect(result.current.updateSnapshot('agent-1', patchData)).rejects.toThrow('boom');
    });
    expect(updateSnapshotMock).toHaveBeenCalledWith('agent-1', patchData);
    expect(message.error).toHaveBeenCalled();
    expect(message.success).not.toHaveBeenCalled();
  });

  it('resolves and reloads on updateSnapshot success', async () => {
    updateSnapshotMock.mockResolvedValue(undefined);
    const { result } = renderHook(() => useSnapshotsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.updateSnapshot('agent-1', patchData);
    });
    expect(updateSnapshotMock).toHaveBeenCalledWith('agent-1', patchData);
    expect(message.success).toHaveBeenCalled();
    expect(listSnapshotsMock).toHaveBeenCalledTimes(2);
  });
});
