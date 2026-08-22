import { fireEvent, render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { WorkspaceConfigForm } from '../WorkspaceConfigForm';

const Harness = ({
  initialValues = {},
  chatModels = [],
}: {
  initialValues?: Record<string, unknown>;
  chatModels?: string[];
}) => {
  const [form] = Form.useForm();
  return (
    <Form form={form} initialValues={initialValues}>
      <WorkspaceConfigForm form={form} loading={false} chatModels={chatModels} onSubmit={vi.fn()} />
    </Form>
  );
};

describe('WorkspaceConfigForm', () => {
  it('shows default placeholders for every unset field', () => {
    render(<Harness />);
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

  it('hides placeholders once values are set', () => {
    render(
      <Harness initialValues={{ query_mode: 'hybrid', top_k: 8 }} />,
    );
    expect(screen.queryByText('默认：混合检索')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('默认：5')).not.toBeInTheDocument();
  });

  it('treats explicit 0 as a set value, not unset', () => {
    render(
      <Harness initialValues={{ score_threshold: 0, rerank_top_k: 0 }} />,
    );
    expect(screen.queryByPlaceholderText('默认：0（不启用）')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('默认：0（跟随 Top-K）')).not.toBeInTheDocument();
  });

  it('renders judge_model select fed by chat catalogue', () => {
    render(<Harness chatModels={['qwen-turbo', 'qwen-plus']} />);
    // antd 5 Select 的 combobox role 在内部 input 上，label 关联用 getByLabelText 命中
    fireEvent.mouseDown(screen.getByLabelText('判断模型'));
    expect(screen.getByRole('option', { name: 'qwen-turbo' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'qwen-plus' })).toBeInTheDocument();
  });

  it('hides rerank_model unless reranking is builtin-score-v1', () => {
    render(<Harness initialValues={{ reranking: 'vector' }} chatModels={['qwen-turbo']} />);
    expect(screen.queryByLabelText('重排模型')).not.toBeInTheDocument();
  });

  it('shows rerank_model with chat catalogue options for builtin-score-v1', () => {
    render(<Harness initialValues={{ reranking: 'builtin-score-v1' }} chatModels={['qwen-turbo', 'qwen-plus']} />);
    fireEvent.mouseDown(screen.getByLabelText('重排模型'));
    expect(screen.getByRole('option', { name: 'qwen-turbo' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'qwen-plus' })).toBeInTheDocument();
  });
});
