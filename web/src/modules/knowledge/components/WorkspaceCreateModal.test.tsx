import { fireEvent, render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkspaceCreateModal } from './WorkspaceCreateModal';

import { llmApi } from '@/modules/llm';

// ModelSelect 自取 embedding 模型目录（/admin/models + /admin/providers）：走
// barrel mock，否则真实 fetch 网络挂测试。option 恒带健康徽章 → accessible name
// 为「managed-embedding 未探活」，断言用正则匹配模型名。
vi.mock('@/modules/llm', () => ({
  llmApi: {
    listModels: vi.fn(),
    listProviders: vi.fn(),
  },
}));

const Harness = () => {
  const [form] = Form.useForm();
  return (
    <WorkspaceCreateModal
      open
      loading={false}
      form={form}
      editorCandidates={[]}
      onClose={vi.fn()}
      onSubmit={vi.fn()}
    />
  );
};

describe('WorkspaceCreateModal', () => {
  beforeEach(() => {
    vi.mocked(llmApi.listModels).mockReset().mockResolvedValue([
      { providerId: 'p1', name: 'managed-embedding', capabilities: ['embedding'], enabled: true },
    ]);
    vi.mocked(llmApi.listProviders).mockReset().mockResolvedValue([{ id: 'p1', name: '托管厂商' }]);
  });

  it('renders only managed embedding models', async () => {
    render(<Harness />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '嵌入模型' }));
    expect(await screen.findByRole('option', { name: /managed-embedding/ })).toBeInTheDocument();
    expect(screen.queryByText(/text-embedding-v3/)).not.toBeInTheDocument();
  });
});
