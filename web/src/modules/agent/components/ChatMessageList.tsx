import { RobotOutlined, ThunderboltOutlined, UserOutlined } from '@ant-design/icons';
import { Empty, Skeleton, Spin, Typography } from 'antd';
import type { RefObject } from 'react';

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
}

export const ChatMessageList = ({
  messages,
  loadingMsgs,
  sending,
  selectedConv,
  selectedAgent,
  bottomRef,
}: Props) => (
  <div
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
              (m.role === 'agent' ? (
                <RobotOutlined style={{ color: '#1677ff' }} />
              ) : (
                <ThunderboltOutlined style={{ color: '#ff4d4f' }} />
              ))}
            <Text type="secondary" style={{ fontSize: 11 }}>
              {m.role === 'user' ? '你' : m.role === 'agent' ? 'Agent' : '错误'}
            </Text>
            {m.role === 'user' && <UserOutlined style={{ color: '#8c8c8c' }} />}
          </div>
          <div style={BUBBLE[m.role] || BUBBLE.agent}>
            {m.role === 'user' ? (
              <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{m.content}</span>
            ) : (
              <ChatMarkdown content={m.content || ''} />
            )}
            {m.role === 'agent' && <ChatStepList steps={m.steps} />}
          </div>
        </div>
      ))}
    {sending && (
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
