import { fireEvent, render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { AgentFormSections } from './AgentFormSections';

describe('AgentFormSections', () => {
  it('limits max iterations to the product range with a slider', () => {
    render(
      <Form initialValues={{ maxIterations: 10 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    const slider = screen.getByRole('slider', { name: '最大迭代次数' });
    expect(slider).toHaveAttribute('aria-valuemin', '1');
    expect(slider).toHaveAttribute('aria-valuemax', '90');
    expect(slider).toHaveAttribute('aria-valuenow', '10');
  });

  it('renders only managed chat models', async () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'managed-chat', label: 'managed-chat', reasoning: false }] }]}
        />
      </Form>,
    );

    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'LLM 模型' }));
    expect(screen.getByRole('option', { name: 'managed-chat' })).toBeInTheDocument();
    expect(screen.queryByText(/qwen-plus/)).not.toBeInTheDocument();
  });

  it('allows filtering a large model catalogue by name', () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: 'Qwen', models: [{ value: 'qwen-max', label: 'qwen-max', reasoning: false }] }]}
        />
      </Form>,
    );

    expect(screen.getByRole('combobox', { name: 'LLM 模型' })).not.toHaveAttribute('readonly');
  });

  it('labels an unavailable current model without adding it to new forms', () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'managed-chat', label: 'managed-chat', reasoning: false }] }]}
          currentModel="retired-chat"
        />
      </Form>,
    );

    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'LLM 模型' }));
    expect(screen.getByText('retired-chat（当前不可用）')).toBeInTheDocument();
  });

  it('shows an editable max_tokens field for the system assistant', () => {
    render(
      <Form initialValues={{ max_tokens: 2048 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} isSystem />
      </Form>,
    );

    const input = screen.getByLabelText(/最大生成 Token/);
    expect(input).toHaveValue('2048');
    expect(screen.getByText('0 = 不修改（保留现有值）；未设置过则使用平台默认')).toBeInTheDocument();

    fireEvent.change(input, { target: { value: '4096' } });
    expect(input).toHaveValue('4096');
    expect(screen.queryByText(/请输入最大生成 Token/)).not.toBeInTheDocument();
  });

  it('keeps max_tokens visible for ordinary agents', () => {
    render(
      <Form initialValues={{ max_tokens: 2048 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );

    const input = screen.getByLabelText(/最大生成 Token/);
    expect(input).toHaveValue('2048');
  });

  it('hides temperature and compaction fields for the system assistant only', () => {
    const { rerender } = render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} isSystem />
      </Form>,
    );

    expect(screen.queryByRole('slider', { name: 'temperature' })).not.toBeInTheDocument();
    expect(screen.queryByText('压缩最近轮数（compaction_recent_groups）')).not.toBeInTheDocument();
    expect(screen.queryByText('压缩安全比例（compaction_safety_ratio）')).not.toBeInTheDocument();

    rerender(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );

    expect(screen.getByRole('slider', { name: 'temperature' })).toBeInTheDocument();
    expect(screen.getByText('压缩最近轮数（compaction_recent_groups）')).toBeInTheDocument();
  });

  it('shows reasoning effort selector only for reasoning-capable models', () => {
    // 推理模型 → 显示
    render(
      <Form initialValues={{ llmModel: 'o3-mini' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[
            {
              provider: '托管厂商',
              models: [
                { value: 'o3-mini', label: 'o3-mini', reasoning: true },
                { value: 'qwen-turbo', label: 'qwen-turbo', reasoning: false },
              ],
            },
          ]}
        />
      </Form>,
    );
    expect(screen.getByText('思考强度（reasoning_effort）')).toBeInTheDocument();
  });

  it('hides reasoning effort selector for non-reasoning models and for the system assistant', () => {
    // 非推理模型 → 隐藏
    const { rerender } = render(
      <Form initialValues={{ llmModel: 'qwen-turbo' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[
            {
              provider: '托管厂商',
              models: [
                { value: 'o3-mini', label: 'o3-mini', reasoning: true },
                { value: 'qwen-turbo', label: 'qwen-turbo', reasoning: false },
              ],
            },
          ]}
        />
      </Form>,
    );
    expect(screen.queryByText('思考强度（reasoning_effort）')).not.toBeInTheDocument();

    // 推理模型但系统助手 → 隐藏（system 走统一模型管理，不带采样参数）
    rerender(
      <Form initialValues={{ llmModel: 'o3-mini' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[
            {
              provider: '托管厂商',
              models: [{ value: 'o3-mini', label: 'o3-mini', reasoning: true }],
            },
          ]}
          isSystem
        />
      </Form>,
    );
    expect(screen.queryByText('思考强度（reasoning_effort）')).not.toBeInTheDocument();
  });

  it('applies constant bounds to the token inputs', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} isSystem />
      </Form>,
    );

    const maxTokens = screen.getByLabelText(/最大生成 Token/);
    expect(maxTokens).toHaveAttribute('aria-valuemin', '0');
    expect(maxTokens).toHaveAttribute('aria-valuemax', '131072');
    expect(maxTokens).toHaveAttribute('step', '256');

    const contextTokens = screen.getByLabelText(/最大上下文 Token/);
    expect(contextTokens).toHaveAttribute('aria-valuemin', '0');
    expect(contextTokens).toHaveAttribute('aria-valuemax', '128000');
    expect(contextTokens).toHaveAttribute('step', '1000');
  });
});
