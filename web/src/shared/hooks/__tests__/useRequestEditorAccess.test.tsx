import { act, renderHook } from '@testing-library/react';
import { message } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { useRequestEditorAccess } from '../useRequestEditorAccess';

import { operationProposalApi } from '@/modules/operation-gate';

vi.mock('antd', async (importOriginal) => {
  const mod = await importOriginal<typeof import('antd')>();
  return { ...mod, message: { success: vi.fn(), error: vi.fn() } };
});
vi.mock('@/modules/operation-gate', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/modules/operation-gate')>();
  return {
    ...mod,
    operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({ status: 'pending_approval' }) },
  };
});

describe('useRequestEditorAccess', () => {
  it('发起申请并提示成功', async () => {
    const { result } = renderHook(() => useRequestEditorAccess('workflow', 'wf-1', { resourceName: 'My WF' }));
    let ok = false;
    await act(async () => { ok = await result.current.request(); });
    expect(ok).toBe(true);
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalledWith('workflow', 'wf-1', { resourceName: 'My WF' });
    expect(message.success).toHaveBeenCalled();
  });

  it('请求失败返回 false 并提示错误', async () => {
    vi.mocked(operationProposalApi.requestEditorAccess).mockRejectedValueOnce({ response: { data: { error: 'boom' } } });
    const { result } = renderHook(() => useRequestEditorAccess('mcp', 'm-1'));
    let ok: boolean | null = null;
    await act(async () => { ok = await result.current.request(); });
    expect(ok).toBe(false);
    expect(message.error).toHaveBeenCalledWith({ content: 'boom', duration: 3 });
  });
});
