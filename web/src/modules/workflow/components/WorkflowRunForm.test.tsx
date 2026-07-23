import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { WorkflowRunForm } from './WorkflowRunForm';

beforeAll(() => vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))));

const schema = {
  task_label: '研究任务', task_description: '描述本次研究目标',
  fields: [
    { key: 'short', label: '短文本', type: 'short_text' as const, required: true, description: '', options: [] },
    { key: 'long', label: '长文本', type: 'long_text' as const, required: false, description: '', options: [] },
    { key: 'count', label: '数字', type: 'number' as const, required: false, description: '', options: [] },
    { key: 'single', label: '单选', type: 'single_select' as const, required: false, description: '', options: [{ label: '甲', value: 'a' }] },
    { key: 'multi', label: '多选', type: 'multi_select' as const, required: false, description: '', options: [{ label: '乙', value: 'b' }] },
    { key: 'enabled', label: '开关', type: 'boolean' as const, required: false, description: '', options: [] },
    { key: 'date', label: '日期', type: 'date' as const, required: false, description: '', options: [] },
  ],
};

describe('WorkflowRunForm', () => {
  it('renders all seven supported field controls without file input', () => {
    const { container } = render(<WorkflowRunForm schema={schema} loading={false} onSubmit={vi.fn()} />);
    for (const label of ['研究任务', '短文本', '长文本', '数字', '单选', '多选', '开关', '日期']) expect(screen.getByText(label)).toBeInTheDocument();
    expect(container.querySelector('input[type="file"]')).not.toBeInTheDocument();
  });

  it('enforces required values and disables duplicate submission while loading', async () => {
    const { rerender } = render(<WorkflowRunForm schema={schema} loading={false} onSubmit={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '开始运行' }));
    expect(await screen.findByText('请输入研究任务')).toBeInTheDocument();
    expect(await screen.findByText('请填写短文本')).toBeInTheDocument();
    rerender(<WorkflowRunForm schema={schema} loading onSubmit={vi.fn()} />);
    expect(screen.getByRole('button', { name: '开始运行' })).toBeDisabled();
  });
});
