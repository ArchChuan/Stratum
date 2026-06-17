import { RobotOutlined } from '@ant-design/icons';
import { Tag, Typography } from 'antd';

import type { Agent } from '../model/agent';

const { Text } = Typography;

interface Props {
  agent?: Agent;
}

export const ChatHeader = ({ agent }: Props) => (
  <div
    style={{
      height: 48,
      background: '#fff',
      borderBottom: '1px solid #f0f0f0',
      display: 'flex',
      alignItems: 'center',
      padding: '0 20px',
      gap: 10,
      flexShrink: 0,
    }}
  >
    <RobotOutlined style={{ fontSize: 18, color: '#1677ff' }} />
    <Text strong style={{ fontSize: 15 }}>
      {agent?.name || '请选择 Agent'}
    </Text>
    {agent?.llmModel && (
      <Tag color="blue" style={{ fontSize: 11 }}>
        {agent.llmModel}
      </Tag>
    )}
    {agent?.description && (
      <Text type="secondary" style={{ fontSize: 12 }} ellipsis>
        {agent.description}
      </Text>
    )}
  </div>
);
