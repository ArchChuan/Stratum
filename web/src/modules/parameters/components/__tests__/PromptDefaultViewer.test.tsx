import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { parametersApi } from '../../api/parameters.api';
import { PromptDefaultViewer } from '../PromptDefaultViewer';

vi.mock('antd', async (importOriginal) => {
  const antd = await importOriginal<typeof import('antd')>();
  return { ...antd, message: { success: vi.fn(), error: vi.fn() } };
});

vi.mock('../../api/parameters.api', () => ({
  parametersApi: { promptDefaults: vi.fn() },
}));

const TEMPLATE = '压缩对话历史，保留关键决策。';

describe('PromptDefaultViewer', () => {
  it('opens a modal and renders the full template from the endpoint', async () => {
    vi.mocked(parametersApi.promptDefaults).mockResolvedValue({
      'agent.compaction_prompt': TEMPLATE,
    });

    render(<PromptDefaultViewer promptKey="agent.compaction_prompt" />);
    fireEvent.click(screen.getByRole('button', { name: '查看默认提示词' }));

    expect(await screen.findByDisplayValue(TEMPLATE)).toBeInTheDocument();
    expect(screen.getByText(/未配置时执行按此模板兜底/)).toBeInTheDocument();
  });

  it('copies the full template text', async () => {
    vi.mocked(parametersApi.promptDefaults).mockResolvedValue({
      'agent.compaction_prompt': TEMPLATE,
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });

    render(<PromptDefaultViewer promptKey="agent.compaction_prompt" />);
    fireEvent.click(screen.getByRole('button', { name: '查看默认提示词' }));
    fireEvent.click(await screen.findByRole('button', { name: '复制全文' }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(TEMPLATE));
  });
});
