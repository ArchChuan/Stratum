import { beforeEach, describe, expect, it, vi } from 'vitest';

import { llmApi } from './llm.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: { delete: vi.fn(), get: vi.fn(), patch: vi.fn(), post: vi.fn(), put: vi.fn() },
}));

describe('llmApi', () => {
  beforeEach(() => {
    vi.mocked(api.delete).mockReset();
    vi.mocked(api.delete).mockResolvedValue({} as never);
    vi.mocked(api.get).mockReset();
    vi.mocked(api.patch).mockReset();
    vi.mocked(api.post).mockReset();
    vi.mocked(api.put).mockReset();
  });

  it('toggles a model', async () => {
    await llmApi.toggleModel('m1', false);

    expect(api.patch).toHaveBeenCalledWith('/admin/models/m1/toggle', { enabled: false });
  });
});
