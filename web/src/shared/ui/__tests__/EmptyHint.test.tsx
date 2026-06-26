import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { EmptyHint } from '../EmptyHint';

describe('EmptyHint', () => {
  it('renders title and description', () => {
    render(<EmptyHint title="无数据" description="先创建一个" />);
    expect(screen.getByText('无数据')).toBeInTheDocument();
    expect(screen.getByText('先创建一个')).toBeInTheDocument();
  });

  it('renders action button when label and handler are provided', () => {
    const onAction = vi.fn();
    render(<EmptyHint actionLabel="新建" onAction={onAction} />);
    fireEvent.click(screen.getByRole('button', { name: '新建' }));
    expect(onAction).toHaveBeenCalledTimes(1);
  });

  it('hides action button when handler is missing', () => {
    render(<EmptyHint actionLabel="新建" />);
    expect(screen.queryByRole('button', { name: '新建' })).toBeNull();
  });
});
