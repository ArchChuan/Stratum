import { fireEvent, render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { WorkspaceCreateModal } from './WorkspaceCreateModal';

const Harness = ({ models }: { models: string[] }) => {
  const [form] = Form.useForm();
  return (
    <WorkspaceCreateModal
      open
      loading={false}
      form={form}
      embeddingModels={models}
      onClose={vi.fn()}
      onSubmit={vi.fn()}
    />
  );
};

describe('WorkspaceCreateModal', () => {
  it('renders only managed embedding models', () => {
    render(<Harness models={['managed-embedding']} />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '嵌入模型' }));
    expect(screen.getByRole('option', { name: 'managed-embedding' })).toBeInTheDocument();
    expect(screen.queryByText(/text-embedding-v3/)).not.toBeInTheDocument();
  });
});
