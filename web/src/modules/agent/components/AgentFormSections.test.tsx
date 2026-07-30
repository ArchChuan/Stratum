import { fireEvent, render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { AgentFormSections } from './AgentFormSections';

describe('AgentFormSections', () => {
  it('limits max iterations to the product range with a slider', () => {
    render(<Form initialValues={{ maxIterations: 10 }}><AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} groupedModels={[]} /></Form>);
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
          groupedModels={[{ provider: 'test', models: [{ value: 'managed-chat', label: 'managed-chat' }] }]}
        />
      </Form>,
    );

    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'LLM 模型' }));
    expect(screen.getByRole('option', { name: 'managed-chat' })).toBeInTheDocument();
    expect(screen.queryByText(/qwen-plus/)).not.toBeInTheDocument();
  });

  it('labels an unavailable current model without adding it to new forms', () => {
    render(
      <Form>
        <AgentFormSections
          skills={[]}
          mcpTools={[]}
          workspaces={[]}
          groupedModels={[{ provider: 'test', models: [{ value: 'managed-chat', label: 'managed-chat' }] }]}
          currentModel="retired-chat"
        />
      </Form>,
    );

    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'LLM 模型' }));
    expect(screen.getByText('retired-chat（当前不可用）')).toBeInTheDocument();
  });
});
