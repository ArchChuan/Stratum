import {
  DeleteOutlined,
  EditOutlined,
  PlayCircleOutlined,
  RobotOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Button, Card, Popconfirm, Space, Tag, Tooltip, Typography } from 'antd';

import type { Agent } from '../model/agent';

const { Text, Paragraph } = Typography;

const MODEL_COLORS: Record<string, string> = {
  'gpt-4': '#1677ff',
  'gpt-3.5-turbo': '#52c41a',
  'glm-4': '#722ed1',
  'glm-4-flash': '#13c2c2',
  'qwen-plus': '#fa8c16',
  'qwen-turbo': '#fa541c',
};

const modelColor = (m: string | undefined) => MODEL_COLORS[m || ''] || '#8c8c8c';

interface AgentCardProps {
  agent: Agent;
  onExecute: (a: Agent) => void;
  onEdit: (a: Agent) => void;
  onDelete: (id: string, name: string) => void;
}

export const AgentCard = ({ agent, onExecute, onEdit, onDelete }: AgentCardProps) => (
  <Card
    style={{
      borderRadius: 12,
      border: '1px solid #f0f0f0',
      height: '100%',
      display: 'flex',
      flexDirection: 'column',
    }}
    styles={{ body: { padding: 20, flex: 1, display: 'flex', flexDirection: 'column' } }}
    hoverable
  >
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        marginBottom: 12,
      }}
    >
      <div
        style={{
          width: 40,
          height: 40,
          borderRadius: 10,
          background: 'linear-gradient(135deg, #e6f4ff 0%, #f9f0ff 100%)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <RobotOutlined style={{ fontSize: 18, color: '#1677ff' }} />
      </div>
      <Tag
        style={{
          border: 'none',
          borderRadius: 6,
          fontSize: 11,
          background: `${modelColor(agent.llmModel)}18`,
          color: modelColor(agent.llmModel),
          fontWeight: 500,
        }}
      >
        {agent.llmModel}
      </Tag>
    </div>

    <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>
      {agent.name}
    </Text>
    <Paragraph
      type="secondary"
      ellipsis={{ rows: 2 }}
      style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
    >
      {agent.description || '暂无描述'}
    </Paragraph>

    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        paddingTop: 12,
        borderTop: '1px solid #f5f5f5',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <ThunderboltOutlined style={{ color: '#8c8c8c', fontSize: 12 }} />
        <Text type="secondary" style={{ fontSize: 12 }}>
          {agent.allowedSkills?.length || 0} 技能
        </Text>
      </div>
      <Space size={4}>
        <Tooltip title="执行">
          <Button
            type="text"
            size="small"
            icon={<PlayCircleOutlined />}
            onClick={() => onExecute(agent)}
            style={{ color: '#1677ff' }}
          />
        </Tooltip>
        <Tooltip title="编辑">
          <Button
            type="text"
            size="small"
            icon={<EditOutlined />}
            onClick={() => onEdit(agent)}
          />
        </Tooltip>
        <Tooltip title="删除">
          <Popconfirm
            title={`确定删除 "${agent.name}" 吗？`}
            onConfirm={() => onDelete(agent.id, agent.name)}
            okText="删除"
            okType="danger"
            cancelText="取消"
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Tooltip>
      </Space>
    </div>
  </Card>
);
