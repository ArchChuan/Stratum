import { beforeEach, describe, expect, it, vi } from 'vitest';

import { promptApi } from './prompt.api';

const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('@/services/client', () => ({ default: client }));

const summaryPayload = {
  key: 'system_prompt',
  latest_version: 3,
  latest_status: 'published',
  created_at: '2026-08-09T11:00:00Z',
};

const templatePayload = {
  key: 'system_prompt',
  tenant_id: null,
  version: 3,
  content: 'you are a helpful assistant',
  status: 'published',
  content_hash: 'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789',
  created_by: 'user:1',
  created_at: '2026-08-09T11:00:00Z',
};

const bindingPayload = {
  key: 'system_prompt',
  scope: 'tenant:t1',
  stable_version_id: 'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789',
  canary_version_id: '',
  traffic_percent: 0,
};

describe('prompt api', () => {
  beforeEach(() => {
    client.get.mockReset();
    client.post.mockReset();
    client.put.mockReset();
    client.delete.mockReset();
  });

  it('lists prompt summaries with page params through the shared client', async () => {
    client.get.mockResolvedValue({ data: { prompts: [summaryPayload], total: 1 } });
    const page = await promptApi.listPrompts({ page: 1, pageSize: 10 });
    expect(client.get).toHaveBeenCalledWith('/prompts', { params: { page: 1, page_size: 10 } });
    expect(page.prompts).toHaveLength(1);
    expect(page.prompts[0].latest_version).toBe(3);
    expect(page.total).toBe(1);
  });

  it('normalizes a missing prompts array instead of failing parse', async () => {
    client.get.mockResolvedValue({ data: { prompts: undefined, total: 0 } });
    const page = await promptApi.listPrompts({ page: 1, pageSize: 20 });
    expect(page.prompts).toEqual([]);
  });

  it('creates a prompt template', async () => {
    client.post.mockResolvedValue({ data: { status: 'created' } });
    await promptApi.create({ key: 'k1', content: 'hello' });
    expect(client.post).toHaveBeenCalledWith('/prompts', { key: 'k1', content: 'hello' });
  });

  it('lists versions for a key, encoding the key', async () => {
    client.get.mockResolvedValue({ data: { versions: [templatePayload] } });
    const versions = await promptApi.listVersions('system_prompt');
    expect(client.get).toHaveBeenCalledWith('/prompts/system_prompt/versions');
    expect(versions).toHaveLength(1);
    expect(versions[0].content_hash).toBe(templatePayload.content_hash);
  });

  it('publishes a version', async () => {
    client.post.mockResolvedValue({ data: { status: 'published' } });
    await promptApi.publishVersion('k1', 3);
    expect(client.post).toHaveBeenCalledWith('/prompts/k1/versions/3/publish');
  });

  it('lists bindings', async () => {
    client.get.mockResolvedValue({ data: { bindings: [bindingPayload] } });
    const bindings = await promptApi.listBindings();
    expect(client.get).toHaveBeenCalledWith('/prompts/bindings');
    expect(bindings).toHaveLength(1);
    expect(bindings[0].scope).toBe('tenant:t1');
  });

  it('upserts a binding with content-hash version ids', async () => {
    client.put.mockResolvedValue({ data: { status: 'ok' } });
    await promptApi.upsertBinding({
      key: 'k1',
      scope: 'tenant:t1',
      stable_version_id: bindingPayload.stable_version_id,
      canary_version_id: 'cafebabe',
      traffic_percent: 20,
    });
    expect(client.put).toHaveBeenCalledWith('/prompts/bindings', {
      key: 'k1',
      scope: 'tenant:t1',
      stable_version_id: bindingPayload.stable_version_id,
      canary_version_id: 'cafebabe',
      traffic_percent: 20,
    });
  });

  it('deletes a binding by key and scope, encoding both', async () => {
    client.delete.mockResolvedValue({ data: { status: 'ok' } });
    await promptApi.deleteBinding('k1', 'tenant:t1');
    // 冒号在路径段中被 encodeURIComponent 转义，gin 侧会自动解码回 tenant:t1。
    expect(client.delete).toHaveBeenCalledWith('/prompts/bindings/k1/tenant%3At1');
  });

  it('rejects a summary row whose key is missing', async () => {
    client.get.mockResolvedValue({ data: { prompts: [{ latest_version: 1, latest_status: 'draft', created_at: 'x' }], total: 1 } });
    await expect(promptApi.listPrompts({ page: 1, pageSize: 20 })).rejects.toThrow();
  });
});
