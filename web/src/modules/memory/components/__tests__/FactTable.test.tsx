import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { FactTable } from '../FactTable';

const { listFactsMock } = vi.hoisted(() => ({ listFactsMock: vi.fn() }));
vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: { listFacts: listFactsMock, deleteFact: vi.fn(), updateFact: vi.fn() },
}));

describe('FactTable', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listFactsMock.mockResolvedValue({ facts: [], total: 0 });
  });

  it('keeps the search box controlled and submits the keyword on Enter without firing per-keystroke requests', async () => {
    render(<FactTable />);
    await waitFor(() => expect(listFactsMock).toHaveBeenCalledTimes(1));

    const input = screen.getByPlaceholderText('搜索事实内容') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'dark' } });
    expect(input.value).toBe('dark');
    // 输入过程不逐键请求
    expect(listFactsMock).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() =>
      expect(listFactsMock).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'dark' })),
    );
  });
});
