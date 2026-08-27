import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ChatMarkdown } from '../ChatMarkdown';

// 工具引用标记是给模型/对账器的机器指令，渲染前剥离（纯显示层，不改消息源）；
// 对账结果由 FactCheckNotice 透出。两种形式 <tool_ref:ID> 与兼容形式 [tool:ID]
// 均需剥离，正文其余内容原样保留。
describe('ChatMarkdown tool reference stripping', () => {
  it('strips <tool_ref:ID> markers from rendered content', () => {
    render(<ChatMarkdown content="订单已删除 <tool_ref:call_123>" />);
    expect(screen.queryByText(/tool_ref/)).not.toBeInTheDocument();
    expect(screen.getByText(/订单已删除/)).toBeInTheDocument();
  });

  it('strips the legacy [tool:ID] form', () => {
    render(<ChatMarkdown content="通知已发送 [tool:call_456]" />);
    expect(screen.queryByText(/\[tool:/)).not.toBeInTheDocument();
    expect(screen.getByText(/通知已发送/)).toBeInTheDocument();
  });

  it('strips multiple markers mixed into a sentence', () => {
    render(<ChatMarkdown content="先 <tool_ref:call_1> 再 [tool:call_2] 完成" />);
    expect(screen.queryByText(/tool_ref|\[tool:/)).not.toBeInTheDocument();
    expect(screen.getByText(/先 再 完成/)).toBeInTheDocument();
  });

  it('leaves content without markers untouched', () => {
    render(<ChatMarkdown content="没有引用的普通回答" />);
    expect(screen.getByText(/没有引用的普通回答/)).toBeInTheDocument();
  });
});
