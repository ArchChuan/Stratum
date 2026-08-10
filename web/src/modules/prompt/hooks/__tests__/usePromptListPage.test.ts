import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { usePromptListPage } from '../usePromptListPage';

const listPrompts = vi.hoisted(() => vi.fn());
const create = vi.hoisted(() => vi.fn());
vi.mock('../../api/prompt.api', () => ({ promptApi: { listPrompts, create } }));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

const summary = {
  key: 'system_prompt',
  latest_version: 3,
  latest_status: 'published',
  created_at: '2026-08-09T11:00:00Z',
};

describe('usePromptListPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPrompts.mockResolvedValue({ prompts: [summary], total: 1 });
    create.mockResolvedValue({ data: { status: 'created' } });
  });

  it('loads the first page of prompt summaries', async () => {
    const { result } = renderHook(() => usePromptListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listPrompts).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
    expect(result.current.prompts).toEqual([summary]);
    expect(result.current.total).toBe(1);
    expect(result.current.page).toBe(1);
  });

  it('reports a persistent error when the list fails', async () => {
    listPrompts.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => usePromptListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载提示词模板失败', duration: 0 });
    expect(result.current.prompts).toEqual([]);
  });

  it('refetches the new page on page change', async () => {
    const { result } = renderHook(() => usePromptListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    listPrompts.mockResolvedValue({ prompts: [], total: 2 });

    await result.current.handlePageChange(2, 10);
    await waitFor(() => expect(result.current.page).toBe(2));
    expect(listPrompts).toHaveBeenLastCalledWith({ page: 2, pageSize: 10 });
    expect(result.current.total).toBe(2);
  });

  it('creates a template, closes the modal and reloads the list', async () => {
    const { result } = renderHook(() => usePromptListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.openCreate());
    expect(result.current.createOpen).toBe(true);

    await result.current.handleCreate('k1', 'hello');
    expect(create).toHaveBeenCalledWith({ key: 'k1', content: 'hello' });
    await waitFor(() => expect(result.current.createOpen).toBe(false));
    expect(message.success).toHaveBeenCalledWith({ content: '模板已创建', duration: 2 });
    expect(listPrompts).toHaveBeenCalledTimes(2);
  });

  it('reports a persistent error when create fails and keeps the modal open', async () => {
    create.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => usePromptListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.openCreate());

    await result.current.handleCreate('k1', 'hello');
    expect(message.error).toHaveBeenCalledWith({ content: '创建模板失败', duration: 0 });
    expect(result.current.createOpen).toBe(true);
  });
});
