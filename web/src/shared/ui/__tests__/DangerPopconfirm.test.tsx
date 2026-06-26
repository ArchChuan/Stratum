import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
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
    const okBtn = await screen.findByRole('button', { name: /删除/ });
    fireEvent.click(okBtn);
    expect(onConfirm).toHaveBeenCalled();
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
