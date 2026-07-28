import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { agentApi } from '../../api/agent.api';
import { SystemAssistantSettingsForm } from '../SystemAssistantSettingsForm';

vi.mock('../../api/agent.api', () => ({
  agentApi: {
    getSystemSettings: vi.fn(),
    updateSystemSettings: vi.fn(),
  },
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })));
});

describe('SystemAssistantSettingsForm', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders only the model field and preserves an unavailable current model', async () => {
    vi.mocked(agentApi.getSystemSettings).mockResolvedValue({
      agentId: 'stratum-platform-assistant',
      llmModel: 'glm-5.2',
      ready: false,
      availableModels: ['qwen-plus'],
    });

    render(<SystemAssistantSettingsForm onCancel={vi.fn()} onSaved={vi.fn()} />);

    expect(await screen.findByText('glm-5.2（当前不可用）')).toBeInTheDocument();
    expect(screen.getAllByRole('combobox')).toHaveLength(1);
    expect(screen.queryByText(/Prompt|Skill|MCP|知识库|Memory/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存修改' })).toBeDisabled();
  });

  it('updates through the system settings API', async () => {
    vi.mocked(agentApi.getSystemSettings).mockResolvedValue({
      agentId: 'stratum-platform-assistant',
      llmModel: 'glm-5.2',
      ready: true,
      availableModels: ['glm-5.2', 'qwen-plus'],
    });
    vi.mocked(agentApi.updateSystemSettings).mockResolvedValue({
      agentId: 'stratum-platform-assistant',
      llmModel: 'qwen-plus',
      ready: true,
      availableModels: ['glm-5.2', 'qwen-plus'],
    });
    const onSaved = vi.fn();

    render(<SystemAssistantSettingsForm onCancel={vi.fn()} onSaved={onSaved} />);
    await screen.findByText('glm-5.2');
    fireEvent.mouseDown(screen.getByRole('combobox'));
    const qwenOptions = await screen.findAllByText('qwen-plus');
    fireEvent.click(qwenOptions[qwenOptions.length - 1]);
    fireEvent.click(screen.getByRole('button', { name: '保存修改' }));

    await waitFor(() => expect(agentApi.updateSystemSettings).toHaveBeenCalledWith({
      llmModel: 'qwen-plus',
    }));
    expect(onSaved).toHaveBeenCalledWith('qwen-plus');
  });
});
