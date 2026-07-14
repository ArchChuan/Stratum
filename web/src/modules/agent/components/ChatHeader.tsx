import { MenuOutlined, RobotOutlined } from '@ant-design/icons';
import { Button, Tag, Typography } from 'antd';

import type { Agent } from '../model/agent';

const { Text } = Typography;

interface Props {
  agent?: Agent;
  isMobile?: boolean;
  onOpenConversations?: () => void;
}

export const ChatHeader = ({ agent, isMobile = false, onOpenConversations }: Props) => (
  <div
    style={{
      height: 48,
      background: '#fff',
      borderBottom: '1px solid #f0f0f0',
      display: 'flex',
      alignItems: 'center',
      padding: isMobile ? '0 12px' : '0 20px',
      gap: 10,
      flexShrink: 0,
    }}
  >
    {isMobile && (
      <Button
        type="text"
        icon={<MenuOutlined />}
        aria-label="打开会话列表"
        onClick={onOpenConversations}
      />
    )}
    <RobotOutlined style={{ fontSize: 18, color: '#1677ff' }} />
    <Text strong style={{ fontSize: 15 }}>
      {agent?.name || '请选择 Agent'}
    </Text>
    {agent?.llmModel && (
      <Tag color="blue" style={{ fontSize: 11 }}>
        {agent.llmModel}
      </Tag>
    )}
    {agent?.description && !isMobile && (
      <Text type="secondary" style={{ fontSize: 12 }} ellipsis>
        {agent.description}
      </Text>
    )}
  </div>
);
