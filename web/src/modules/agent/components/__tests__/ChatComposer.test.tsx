import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ChatComposer } from '../ChatComposer';

// F6：强制停止生成必须有用户可见入口——流式期间由停止按钮替换发送按钮。
const baseProps = {
  input: '',
  setInput: vi.fn(),
  sending: false,
  selectedConv: 'conv-1',
  onSend: vi.fn(),
};

describe('ChatComposer 停止按钮', () => {
  it('非流式时不渲染停止按钮', () => {
    render(<ChatComposer {...baseProps} streaming={false} onStop={vi.fn()} />);
    expect(screen.queryByLabelText('停止生成')).toBeNull();
  });

  it('流式时渲染停止按钮并回调 onStop', () => {
    const onStop = vi.fn();
    render(<ChatComposer {...baseProps} streaming onStop={onStop} />);
    fireEvent.click(screen.getByLabelText('停止生成'));
    expect(onStop).toHaveBeenCalledTimes(1);
  });
});
