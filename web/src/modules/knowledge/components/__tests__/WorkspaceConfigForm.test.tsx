import { render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { WorkspaceConfigForm } from '../WorkspaceConfigForm';

const Harness = ({ initialValues = {} }: { initialValues?: Record<string, unknown> }) => {
  const [form] = Form.useForm();
  return (
    <Form form={form} initialValues={initialValues}>
      <WorkspaceConfigForm form={form} loading={false} onSubmit={vi.fn()} />
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
});
