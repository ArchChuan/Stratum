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

  it('filters a large model catalogue by search text', async () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[
            {
              provider: 'Qwen',
              models: [
                { value: 'qwen-max', label: 'qwen-max', capabilities: ['chat'], reasoning: false },
                { value: 'qwen-plus', label: 'qwen-plus', capabilities: ['chat'], reasoning: false },
              ],
            },
          ]}
        />
      </Form>,
    );

    // Option children 是 ModelOptionLabel 组件（React element），AntD 默认
    // filterOption 无法对其取文本；必须按自定义 filterModelOption 匹配
    // label/value 纯文本，showSearch 才恢复搜索能力。
    const combobox = screen.getByRole('combobox', { name: 'LLM 模型' });
    fireEvent.mouseDown(combobox);
    fireEvent.input(combobox, { target: { value: 'qwen-max' } });
    await waitFor(() => {
      expect(screen.getByRole('option', { name: /qwen-max/ })).toBeInTheDocument();
      expect(screen.queryByRole('option', { name: /qwen-plus/ })).not.toBeInTheDocument();
    });
  });

  it('renders model capability tags in the LLM model dropdown', () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[
            {
              provider: 'Zhipu',
              models: [
                { value: 'glm-5', label: 'GLM-5', capabilities: ['chat', 'tool_use', 'vision'], reasoning: false },
              ],
            },
          ]}
        />
      </Form>,
    );

    // rc-select 双层结构：role=option 的 ARIA 层文本只有 value（小写模型名），
    // ModelOptionLabel 渲染的中文能力标签在 .ant-select-item-option 层（下拉
    // portal 在 document.body，不在组件 container 内；与 pickModelOption helper
    // 的 document 查询一致），必须在该层断言。
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'LLM 模型' }));
    const option = Array.from(document.querySelectorAll('.ant-select-item-option')).find((el) =>
      el.textContent?.includes('GLM-5'),
    )!;
    expect(option.textContent).toContain('对话');
    expect(option.textContent).toContain('工具调用');
    expect(option.textContent).toContain('视觉');
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

  it('shows an editable max_tokens field', () => {
    render(
      <Form initialValues={{ max_tokens: 2048 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
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

  it('keeps max_tokens visible', () => {
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

  it('warns about truncated thinking when the selected model supports reasoning', () => {
    render(
      <Form initialValues={{ llmModel: 'qwq-32b' }}>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: 'Qwen', models: [{ value: 'qwq-32b', label: 'qwq-32b', reasoning: true, maxTokens: 8192 }] }]}
        />
      </Form>,
    );

    expect(screen.getByText(/推理模型该值含思考长度，过低会截断思考/)).toBeInTheDocument();
    expect(screen.getByText(/推荐 8,?192 tokens（模型最大输出）/)).toBeInTheDocument();
  });

  it('omits the reasoning hint for non-reasoning models', () => {
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

    expect(screen.queryByText(/推理模型该值含思考长度/)).not.toBeInTheDocument();
  });

  it('shows temperature field for all agents', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );

    expect(screen.getByRole('slider', { name: 'temperature' })).toBeInTheDocument();
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

  it('hides reasoning effort selector for non-reasoning models', () => {
    render(
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
  });

  it('applies constant bounds to the token inputs', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
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
  });

  it('hides default hints once explicit values are set', () => {
    render(
      <Form initialValues={{ temperature: 0.5 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    expect(screen.queryByText('默认：0.7')).not.toBeInTheDocument();
  });

  it('renders a required memory scope select with user and agent options', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '记忆范围' }));
    expect(screen.getByRole('option', { name: '用户级' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Agent 级' })).toBeInTheDocument();
  });

  it('keeps the memory scope select visible alongside temperature for all agents', () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    // 等化后记忆范围 Select 与温度块对所有 agent 可见。
    expect(screen.getByRole('combobox', { name: '记忆范围' })).toBeInTheDocument();
    expect(screen.getByRole('slider', { name: 'temperature' })).toBeInTheDocument();
  });

  it('blocks submission when memory scope is missing', async () => {
    render(
      <Form>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
        <button type="submit">提交</button>
      </Form>,
    );
    fireEvent.click(screen.getByRole('button', { name: '提交' }));
    expect(await screen.findByText('请选择记忆范围')).toBeInTheDocument();
  });

  it('renders the delegate section and reflects explicit enabled', () => {
    render(
      <Form initialValues={{ delegateEnabled: true }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    expect(screen.getByText('子 Agent 委托')).toBeInTheDocument();
    // 存量默认关闭委托（DB DEFAULT false）；此处显式传 true 断言开启态回显。
    expect(screen.getByRole('switch', { name: '启用子 Agent 委托' })).toHaveAttribute('aria-checked', 'true');
  });

  it('disables delegate depth and default steps when delegation is off', () => {
    render(
      <Form initialValues={{ delegateEnabled: false }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    expect(screen.getByRole('switch', { name: '启用子 Agent 委托' })).toHaveAttribute('aria-checked', 'false');
    // 开关关闭时深度/步数输入禁用，避免「改了不生效」的误导。
    expect(screen.getByLabelText(/最大委托深度/)).toBeDisabled();
    expect(screen.getByLabelText(/委托默认最大推理步数/)).toBeDisabled();
  });

  it('keeps delegate depth and default steps within product bounds', () => {
    render(
      <Form initialValues={{ delegateMaxDepth: 1, delegateDefaultMaxSteps: 5 }}>
        <AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} />
      </Form>,
    );
    // 深度 0=unset → 回落默认 1；clamp 到硬上限 2（pkg/constants/agent.go 同源）。
    const depth = screen.getByLabelText(/最大委托深度/);
    expect(depth).toHaveAttribute('aria-valuemin', '0');
    expect(depth).toHaveAttribute('aria-valuemax', '2');
    expect(depth).toHaveValue('1');
    // 默认步数 0=unset → 回落默认 5；参数硬上限 10。
    const steps = screen.getByLabelText(/委托默认最大推理步数/);
    expect(steps).toHaveAttribute('aria-valuemin', '0');
    expect(steps).toHaveAttribute('aria-valuemax', '10');
    expect(steps).toHaveValue('5');
  });


});
