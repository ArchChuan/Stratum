import { render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { describe, expect, it } from 'vitest';

import { ChatMessageList } from '../ChatMessageList';

// 修复 #326 同族问题：切 tab 时「会话恢复中」不应闪现「新建或选择一个会话」空态。
// 恢复中（loadingConvs=true 且未选中会话）必须渲染 Skeleton；空态只在恢复完成后仍无会话时出现。
const baseProps = {
  messages: [],
  loadingMsgs: false,
  loadingConvs: false,
  sending: false,
  selectedConv: null,
  selectedAgent: 'agent-system',
  bottomRef: createRef<HTMLDivElement>(),
  scrollContainerRef: createRef<HTMLDivElement>(),
  pinnedToBottomRef: { current: true },
  isMobile: false,
};

describe('ChatMessageList restore-state rendering', () => {
  it('shows skeleton while restoring a conversation, never the empty state', () => {
    render(<ChatMessageList {...baseProps} loadingConvs />);

    expect(screen.queryByText('新建或选择一个会话')).not.toBeInTheDocument();
    expect(screen.queryByText('请先选择会话')).not.toBeInTheDocument();
    expect(document.querySelector('.ant-skeleton')).toBeTruthy();
  });

  it('shows the create-or-select empty state only after restore finished without a conversation', () => {
    render(<ChatMessageList {...baseProps} />);

    expect(screen.getByText('新建或选择一个会话')).toBeInTheDocument();
    expect(document.querySelector('.ant-skeleton')).toBeFalsy();
  });

  it('shows the select-agent empty state when no agent was restored', () => {
    render(<ChatMessageList {...baseProps} selectedAgent={null} />);

    expect(screen.getByText('请先选择 Agent')).toBeInTheDocument();
  });
});
