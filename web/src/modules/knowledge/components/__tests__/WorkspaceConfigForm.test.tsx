import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkspaceConfigForm } from '../WorkspaceConfigForm';

import { llmApi, type Model, type Provider } from '@/modules/llm';

// ModelSelect 自取 chat 模型目录（/admin/models + /admin/providers）：走 barrel
// mock，否则真实 fetch 网络挂测试。option 恒带健康徽章 → accessible name 为
// 「qwen-turbo 未探活」，断言用正则匹配模型名。
const CHAT_MODELS = [
  { providerId: 'p1', name: 'qwen-turbo', capabilities: ['chat'], enabled: true } as Model,
  { providerId: 'p1', name: 'qwen-plus', capabilities: ['chat'], enabled: true } as Model,
];
const PROVIDERS = [{ id: 'p1', name: '托管厂商' } as Provider];

vi.mock('@/modules/llm', () => ({
  llmApi: {
    listModels: vi.fn(),
    listProviders: vi.fn(),
  },
}));

// WorkspaceConfigForm 自带 <Form>：Harness 不能再包一层 <Form initialValues>
// （双层 <form> 共享同一实例会破坏 value 绑定——提交时字段值为空）。预写 form
// store，rc-field-form 在字段挂载时消费，等价于页面 configForm 回填。
let harnessForm: ReturnType<typeof Form.useForm>[0] | null = null;
const Harness = ({
  initialValues = {},
  onSubmit = vi.fn(),
}: {
  initialValues?: Record<string, unknown>;
  onSubmit?: () => void;
}) => {
  const [form] = Form.useForm();
  harnessForm = form;
  form.setFieldsValue(initialValues);
  return <WorkspaceConfigForm form={form} loading={false} onSubmit={onSubmit} />;
};

describe('WorkspaceConfigForm', () => {
  beforeEach(() => {
    vi.mocked(llmApi.listModels).mockReset().mockResolvedValue(CHAT_MODELS);
    vi.mocked(llmApi.listProviders).mockReset().mockResolvedValue(PROVIDERS);
  });

  it('shows default placeholders for every unset field', async () => {
    render(<Harness />);
    await act(async () => {});
    // Select 的 placeholder 渲染为 .ant-select-selection-placeholder 文本,非 input attr
    expect(screen.getByText('默认：混合检索')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('默认：512')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('默认：64')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('默认：5')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('默认：0（不启用）')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('默认：0（跟随 Top-K）')).toBeInTheDocument();
    // 重排策略保持既有"关闭" placeholder
    expect(screen.getByText('关闭')).toBeInTheDocument();
  });

  it('hides placeholders once values are set', async () => {
    render(<Harness initialValues={{ query_mode: 'hybrid', top_k: 8 }} />);
    await act(async () => {});
    expect(screen.queryByText('默认：混合检索')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('默认：5')).not.toBeInTheDocument();
  });

  it('treats explicit 0 as a set value, not unset', async () => {
    render(<Harness initialValues={{ score_threshold: 0, rerank_top_k: 0 }} />);
    await act(async () => {});
    expect(screen.queryByPlaceholderText('默认：0（不启用）')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('默认：0（跟随 Top-K）')).not.toBeInTheDocument();
  });

  it('renders judge_model select fed by chat catalogue', async () => {
    render(<Harness />);
    // antd 5 Select 的 combobox role 在内部 input 上，label 关联用 getByLabelText 命中
    fireEvent.mouseDown(screen.getByLabelText('判断模型'));
    expect(await screen.findByRole('option', { name: /qwen-turbo/ })).toBeInTheDocument();
    expect(await screen.findByRole('option', { name: /qwen-plus/ })).toBeInTheDocument();
  });

  it('hides rerank_model unless reranking is builtin-score-v1', async () => {
    render(<Harness initialValues={{ reranking: 'vector' }} />);
    await act(async () => {});
    expect(screen.queryByLabelText('重排模型')).not.toBeInTheDocument();
  });

  it('shows rerank_model with chat catalogue options for builtin-score-v1', async () => {
    render(<Harness initialValues={{ reranking: 'builtin-score-v1' }} />);
    fireEvent.mouseDown(screen.getByLabelText('重排模型'));
    expect(await screen.findByRole('option', { name: /qwen-turbo/ })).toBeInTheDocument();
    expect(await screen.findByRole('option', { name: /qwen-plus/ })).toBeInTheDocument();
  });

  // InputNumber 的 max/min 在 blur 时把越界值钳制回界内（21→20），表单 store
  // 永远收不到 21；rules 是纵深防御层（覆盖程序化注入/未来控件变更），后端 binding
  // 与 domain Validate 才是权威。测试直接注入越界值验证 rules 兜底。
  it('blocks out-of-range top_k on submit', async () => {
    render(<Harness />);
    await act(async () => {});
    act(() => harnessForm!.setFieldsValue({ top_k: 21 }));
    // antd 5 对两汉字按钮自动插入空格,可访问名为 "保 存"（/保\s*存/ 兼容）。
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    expect(await screen.findByText('Top-K 需在 1-20 之间')).toBeInTheDocument();
  });

  it('blocks out-of-range rerank_top_k on submit', async () => {
    render(<Harness />);
    await act(async () => {});
    act(() => harnessForm!.setFieldsValue({ rerank_top_k: 21 }));
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    expect(await screen.findByText('重排 Top-K 需在 0-20 之间')).toBeInTheDocument();
  });

  it('submits valid values within range', async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);
    await act(async () => {});
    const topK = screen.getByLabelText('Top-K') as HTMLInputElement;
    const rerank = screen.getByLabelText('重排 Top-K') as HTMLInputElement;
    // antd 5 InputNumber 在 blur 时才把值提交给表单（typing 期间保持内部态）。
    fireEvent.change(topK, { target: { value: '10' } });
    fireEvent.blur(topK);
    fireEvent.change(rerank, { target: { value: '5' } });
    fireEvent.blur(rerank);
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ top_k: 10, rerank_top_k: 5 })),
    );
  });
});
