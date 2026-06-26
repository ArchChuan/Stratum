import { RobotOutlined, ThunderboltOutlined, UserOutlined } from '@ant-design/icons';
import { Empty, Skeleton, Spin, Tag, Typography } from 'antd';
import { type MutableRefObject, type RefObject, useEffect } from 'react';

import type { ChatMessage } from '../model/agent';

import { BUBBLE, ChatMarkdown } from './ChatMarkdown';
import { ChatStepList } from './ChatStepList';

const { Text } = Typography;

interface Props {
  messages: ChatMessage[];
  loadingMsgs: boolean;
  sending: boolean;
  selectedConv: string | null;
  selectedAgent: string | null;
  bottomRef: RefObject<HTMLDivElement>;
  scrollContainerRef: RefObject<HTMLDivElement>;
  pinnedToBottomRef: MutableRefObject<boolean>;
}

// StreamingBubble renders plain text + blinking cursor during streaming to avoid
// re-parsing ReactMarkdown on every token (~60fps). Once streaming is done the
// parent replaces the message content and the normal ChatMarkdown path renders.
const StreamingBubble = ({ content }: { content: string }) => (
  <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
    {content.replace(/^\n+/, '')}
    <span
      style={{
        display: 'inline-block',
        width: 2,
        height: '1em',
        background: '#1677ff',
        marginLeft: 2,
        verticalAlign: 'text-bottom',
        animation: 'blink 0.8s step-start infinite',
      }}
    />
    <style>{`@keyframes blink { 50% { opacity: 0 } }`}</style>
  </span>
);

export const ChatMessageList = ({
  messages,
  loadingMsgs,
  sending,
  selectedConv,
  selectedAgent,
  bottomRef,
  scrollContainerRef,
  pinnedToBottomRef,
}: Props) => {
  // The last message is the in-flight assistant bubble while streaming.
  const streamingMsgId = sending && messages.length > 0 ? messages[messages.length - 1].id : null;

  // Detect manual scroll: unpin when user scrolls up, re-pin when near bottom.
  useEffect(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const onScroll = () => {
      const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      pinnedToBottomRef.current = distFromBottom < 60;
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, [scrollContainerRef, pinnedToBottomRef]);

  return (
    <div
      ref={scrollContainerRef}
      style={{
        flex: 1,
        overflowY: 'auto',
        padding: '20px 24px',
        display: 'flex',
        flexDirection: 'column',
        gap: 12,
      }}
    >
      {!selectedConv && !loadingMsgs && (
        <Empty
          description={selectedAgent ? '新建或选择一个会话' : '请先选择 Agent'}
          style={{ marginTop: 80 }}
        />
      )}
      {loadingMsgs && <Skeleton active paragraph={{ rows: 6 }} />}
      {!loadingMsgs &&
        messages.map((m) => (
          <div
            key={m.id}
            style={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: m.role === 'user' ? 'flex-end' : 'flex-start',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
              {m.role !== 'user' &&
                (m.role === 'assistant' ? (
                  <RobotOutlined style={{ color: '#1677ff' }} />
                ) : (
                  <ThunderboltOutlined style={{ color: '#ff4d4f' }} />
                ))}
              <Text type="secondary" style={{ fontSize: 11 }}>
                {m.role === 'user' ? '你' : m.role === 'assistant' ? 'Agent' : '错误'}
              </Text>
              {m.role === 'user' && <UserOutlined style={{ color: '#8c8c8c' }} />}
            </div>
            <div style={BUBBLE[m.role] || BUBBLE.assistant}>
              {m.role === 'user' ? (
                <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{m.content}</span>
              ) : m.id === streamingMsgId ? (
                <StreamingBubble content={m.content || ''} />
              ) : (
                <ChatMarkdown content={m.content || ''} />
              )}
              {m.interrupted && (
                <div style={{ marginTop: 6 }}>
                  <Tag color="orange">已中断</Tag>
                </div>
              )}
              {m.role === 'assistant' && m.id !== streamingMsgId && (
                <ChatStepList steps={m.steps} />
              )}
            </div>
          </div>
        ))}
      {sending && messages[messages.length - 1]?.role !== 'assistant' && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            alignSelf: 'flex-start',
          }}
        >
          <RobotOutlined style={{ color: '#1677ff' }} />
          <Spin size="small" />
          <Text type="secondary" style={{ fontSize: 12 }}>
            Agent 正在处理…
          </Text>
        </div>
      )}
      <div ref={bottomRef} />
    </div>
  );
};
