import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { AgentFormSections } from './AgentFormSections';

// rc-select 双层结构：role=option 的 ARIA 层（rc-virtual-list 无障碍包装）不绑定
// onSelect；交互事件绑在 .ant-select-item-option 上。因此模拟选中必须操作
// .ant-select-item-option，getByRole('option') 匹配的是 ARIA 层，点击无效。
const pickModelOption = (container: HTMLElement, label: string) => {
  fireEvent.mouseDown(container.querySelector('.ant-select-selector')!);
  const option = Array.from(document.querySelectorAll('.ant-select-item-option')).find((el) =>
    el.textContent?.includes(label),
  )!;
  fireEvent.mouseDown(option);
  fireEvent.click(option);
};

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
    // 无选中模型（groupedModels 为空）→ 展示平台兜底输出上限
    expect(screen.getByText(/平台兜底 4,?096 tokens/)).toBeInTheDocument();

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

  it('shows the recommended max output when the selected model has a known maxOut', () => {
    render(
      <Form initialValues={{ llmModel: 'glm-5.2' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'glm-5.2', label: 'glm-5.2', reasoning: false, maxTokens: 8192 }] }]}
        />
      </Form>,
    );

    expect(screen.getByText(/推荐 8,?192 tokens（模型最大输出）；0 = 不修改（保留现有值）/)).toBeInTheDocument();
  });

  it('falls back to the platform default text when the selected model has no maxOut', () => {
    render(
      <Form initialValues={{ llmModel: 'retired-chat' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'managed-chat', label: 'managed-chat', reasoning: false, maxTokens: 8192 }] }]}
          currentModel="retired-chat"
        />
      </Form>,
    );

    expect(screen.getByText(/平台兜底 4,?096 tokens/)).toBeInTheDocument();
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

  it('fills recommended context tokens from the selected model window when value is auto', async () => {
    const { container } = render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'glm-5.2', label: 'glm-5.2', reasoning: false, contextWindow: 128000 }] }]}
        />
      </Form>,
    );

    pickModelOption(container, 'glm-5.2');

    // 128000 × 0.85 = 108800，未显式设置时选中模型自动填入推荐值
    await waitFor(() => expect(screen.getByLabelText(/最大上下文 Token/)).toHaveValue('108800'));
  });

  it('keeps an explicit maxContextTokens when the model changes', async () => {
    const { container } = render(
      <Form initialValues={{ maxContextTokens: 50000 }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'glm-5.2', label: 'glm-5.2', reasoning: false, contextWindow: 128000 }] }]}
        />
      </Form>,
    );

    pickModelOption(container, 'glm-5.2');

    // 显式值不被联动覆盖
    await waitFor(() => expect(screen.getByLabelText(/最大上下文 Token/)).toHaveValue('50000'));
  });

  it('does not fill tokens when the selected model has no known window', async () => {
    const { container } = render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'unknown-model', label: 'unknown-model', reasoning: false }] }]}
        />
      </Form>,
    );

    pickModelOption(container, 'unknown-model');

    await waitFor(() => expect(screen.getByLabelText(/最大上下文 Token/)).not.toHaveValue('108800'));
    // 窗口未知 → 仅显示自动解析文案
    expect(screen.getByText('0 = 自动按模型窗口解析')).toBeInTheDocument();
  });

  it('shows the recommended window text when the model window is known', () => {
    render(
      <Form initialValues={{ llmModel: 'glm-5.2' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: '托管厂商', models: [{ value: 'glm-5.2', label: 'glm-5.2', reasoning: false, contextWindow: 128000 }] }]}
        />
      </Form>,
    );

    expect(screen.getByText('推荐 108800 tokens（模型窗口 128000 × 85%）；0 = 自动按模型窗口解析')).toBeInTheDocument();
  });

  it('shows default hints for unset advanced fields', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    expect(screen.getByText('默认：0.7')).toBeInTheDocument();
    expect(screen.getByText('默认：0.8')).toBeInTheDocument();
    expect(screen.getByText('默认：0.3')).toBeInTheDocument();
  });

  it('hides default hints once explicit values are set', () => {
    render(
      <Form initialValues={{ temperature: 0.5, compaction_safety_ratio: 0.9, compaction_temperature: 0.2 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    expect(screen.queryByText('默认：0.7')).not.toBeInTheDocument();
    expect(screen.queryByText('默认：0.8')).not.toBeInTheDocument();
    expect(screen.queryByText('默认：0.3')).not.toBeInTheDocument();
  });

  it('offers 0（自动推导）for compaction_recent_groups without a disable path', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '压缩最近轮数（compaction_recent_groups）' }));
    expect(screen.getByRole('option', { name: '0（自动推导）' })).toBeInTheDocument();
  });

  it('renders compaction prompt viewer next to the empty prompt field', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    // compaction_prompt + AgentMemoryConfig 的 memory.extraction_prompt 各一个
    expect(screen.getAllByRole('button', { name: '查看默认提示词' })).toHaveLength(2);
  });
});
