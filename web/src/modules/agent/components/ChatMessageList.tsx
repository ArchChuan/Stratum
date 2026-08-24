import { RobotOutlined, ThunderboltOutlined, UserOutlined } from '@ant-design/icons';
import { Empty, Skeleton, Spin, Tag, Typography } from 'antd';
import { memo, type MutableRefObject, type RefObject, useEffect } from 'react';

import type { ChatMessage } from '../model/agent';

import { BUBBLE, ChatMarkdown } from './ChatMarkdown';
import { ChatStepList } from './ChatStepList';
import { DiagnosticReport } from './DiagnosticReport';
import NoAnswerNotice from './NoAnswerNotice';
import { ResourceChangeProposalCard } from './ResourceChangeProposalCard';
import { SourceCardList } from './SourceCardList';
import TaskProgressBanner from './TaskProgressBanner';

const { Text } = Typography;

interface Props {
  messages: ChatMessage[];
  loadingMsgs: boolean;
  loadingConvs: boolean;
  sending: boolean;
  selectedConv: string | null;
  selectedAgent: string | null;
  bottomRef: RefObject<HTMLDivElement>;
  scrollContainerRef: RefObject<HTMLDivElement>;
  pinnedToBottomRef: MutableRefObject<boolean>;
  isMobile?: boolean;
  contentSwitching?: boolean;
}

// StreamingBubble renders plain text + blinking cursor during streaming to avoid
// re-parsing ReactMarkdown on every token (~60fps). Once streaming is done the
// parent replaces the message content and the normal ChatMarkdown path renders.
const StreamingBubble = ({ content }: { content: string }) => (
  <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
    {content.replace(/^\n+/, '')}
    <span
      className="chat-stream-cursor"
      style={{
        display: 'inline-block',
        width: 2,
        height: '1em',
        background: '#2563eb',
        marginLeft: 2,
        verticalAlign: 'text-bottom',
      }}
    />
  </span>
);

// memo：流式期间仅最后一条消息内容变化（useChatPage 的 prev.map 只替换
// 流式消息），历史消息 props 引用稳定 → 跳过重渲染，避免每帧重跑
// ReactMarkdown 解析（H2）。
const MessageItem = memo(function MessageItem({
  message: m,
  streaming,
  isMobile,
}: {
  message: ChatMessage;
  streaming: boolean;
  isMobile: boolean;
}) {
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: m.role === 'user' ? 'flex-end' : 'flex-start',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        {m.role !== 'user' &&
          (m.role === 'assistant' ? (
            <RobotOutlined style={{ color: '#2563eb' }} />
          ) : (
            <ThunderboltOutlined style={{ color: '#ff4d4f' }} />
          ))}
        <Text type="secondary" style={{ fontSize: 11 }}>
          {m.role === 'user' ? '你' : m.role === 'assistant' ? 'Agent' : '错误'}
        </Text>
        {m.role === 'user' && <UserOutlined style={{ color: '#8c8c8c' }} />}
      </div>
      <div
        className="chat-message-bubble"
        style={{
          ...(BUBBLE[m.role] || BUBBLE.assistant),
          maxWidth: isMobile ? '88%' : '72%',
          padding: isMobile ? '8px 10px' : '10px 14px',
          overflowWrap: 'anywhere',
          minWidth: 0,
        }}
      >
        {m.role === 'user' ? (
          <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{m.content}</span>
        ) : streaming ? (
          <StreamingBubble content={m.content || ''} />
        ) : (
          <>
            {m.role === 'assistant' && m.taskSnapshot ? (
              <TaskProgressBanner snapshot={m.taskSnapshot} />
            ) : null}
            <ChatMarkdown content={m.content || ''} />
            {m.artifacts?.map((artifact, index) => {
              const artifactCitations = artifact.citations ?? [];
              const report = artifact.diagnosticReport ?? (artifactCitations.length > 0 ? {
                facts: [],
                inferences: [],
                evidenceGaps: [],
                recommendedActions: [],
                citations: artifactCitations,
                steps: [],
              } : undefined);
              if (artifact.resourceChangeProposal) {
                return <ResourceChangeProposalCard key={`proposal-${artifact.resourceChangeProposal.id}`} proposal={artifact.resourceChangeProposal} />;
              }
              return report ? (
                <DiagnosticReport
                  key={`${artifact.type}-${artifact.profileVersion || index}`}
                  report={report}
                  profileVersion={artifact.profileVersion}
                />
              ) : null;
            })}
            {/* P1.4: 回答引用的来源文档卡片（点击预览原文） */}
            <SourceCardList sources={m.sources} />
            {/* P1: 无答案结构化信号 → 拒答提示（nil=有答案不渲染） */}
            {m.role === 'assistant' && m.noAnswer ? <NoAnswerNotice noAnswer={m.noAnswer} /> : null}
          </>
        )}
        {m.interrupted && (
          <div style={{ marginTop: 6 }}>
            <Tag color="orange">已中断</Tag>
          </div>
        )}
        {m.approvalRejected && (
          <div style={{ marginTop: 6 }}>
            <Tag color="volcano">工具审批未通过，该工具未执行</Tag>
          </div>
        )}
        {m.role === 'assistant' && !streaming && (
          <ChatStepList steps={m.steps} />
        )}
      </div>
    </div>
  );
});

export const ChatMessageList = ({
  messages,
  loadingMsgs,
  loadingConvs,
  sending,
  selectedConv,
  selectedAgent,
  bottomRef,
  scrollContainerRef,
  pinnedToBottomRef,
  isMobile = false,
  contentSwitching = false,
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
    <>
      <style>{`
        @keyframes chat-stream-blink { 50% { opacity: 0 } }
        .chat-stream-cursor { animation: chat-stream-blink 0.8s step-start infinite; }
        @media (prefers-reduced-motion: reduce) { .chat-stream-cursor { animation: none; } }
      `}</style>
      <div
        style={{
          position: 'relative',
          flex: 1,
          minWidth: 0,
          minHeight: 0,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        <div
          className="chat-message-list"
          ref={scrollContainerRef}
          style={{
            flex: 1,
            overflowY: 'auto',
            padding: isMobile ? 12 : '20px 24px',
            minWidth: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: 12,
          }}
        >
        {(loadingMsgs || (!selectedConv && loadingConvs)) && <Skeleton active paragraph={{ rows: 6 }} />}
        {!selectedConv && !loadingConvs && !loadingMsgs && (
          <Empty
            description={selectedAgent ? '新建或选择一个会话' : '请先选择 Agent'}
            style={{ marginTop: 80 }}
          />
        )}
        {!loadingMsgs &&
          messages.map((m) => (
            <MessageItem
              key={m.id}
              message={m}
              streaming={m.id === streamingMsgId}
              isMobile={isMobile}
            />
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
            <RobotOutlined style={{ color: '#2563eb' }} />
            <Spin size="small" />
            <Text type="secondary" style={{ fontSize: 12 }}>
              Agent 正在处理…
            </Text>
          </div>
        )}
          <div ref={bottomRef} />
        </div>
        {contentSwitching && <div className="chat-content-blank" aria-hidden="true" />}
      </div>
    </>
  );
};
