import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import { DangerPopconfirm } from '../DangerPopconfirm';

describe('DangerPopconfirm', () => {
  it('renders default delete button', () => {
    render(<DangerPopconfirm onConfirm={() => {}} />);
    const btn = screen.getByRole('button');
    expect(btn).toBeInTheDocument();
  });

  it('confirms via popconfirm flow', async () => {
    const onConfirm = vi.fn();
    render(<DangerPopconfirm onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole('button'));
    const deleteButtons = await screen.findAllByRole('button', { name: /删\s*除/ });
    const okBtn = deleteButtons[deleteButtons.length - 1];
    if (!okBtn) throw new Error('确认按钮未渲染');
    fireEvent.click(okBtn);
    await waitFor(() => expect(onConfirm).toHaveBeenCalled());
  });

  it('renders custom children when provided', () => {
    render(
      <DangerPopconfirm onConfirm={() => {}}>
        <button>自定义</button>
      </DangerPopconfirm>,
    );
    expect(screen.getByRole('button', { name: '自定义' })).toBeInTheDocument();
  });
});
