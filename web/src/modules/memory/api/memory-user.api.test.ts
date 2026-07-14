import { beforeEach, describe, expect, it, vi } from 'vitest';

import { memoryUserApi } from './memory-user.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: { delete: vi.fn() },
}));

describe('memoryUserApi', () => {
  beforeEach(() => {
    vi.mocked(api.delete).mockReset();
    vi.mocked(api.delete).mockResolvedValue({} as never);
  });

  it('clears memories through the backend-relative route', async () => {
    await memoryUserApi.clearMyMemories();

    expect(api.delete).toHaveBeenCalledWith('/memory/clear');
  });
});
