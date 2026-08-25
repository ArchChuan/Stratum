import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlayCircleOutlined,
  RobotOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Button, Card, Popconfirm, Space, Tag, Tooltip, Typography } from 'antd';

import type { Agent } from '../model/agent';

const { Text, Paragraph } = Typography;

const MODEL_COLORS: Record<string, string> = {
  'gpt-4': '#2563eb',
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
  /** 只读「查看配置」入口（普通成员且非白名单编辑人）。 */
  onView?: (a: Agent) => void;
  /** 仅管理员可见删除。 */
  canManage?: boolean;
  /** 白名单成员可直接编辑（无需审批）。 */
  canEdit?: boolean;
}

export const AgentCard = ({
  agent,
  onExecute,
  onEdit,
  onDelete,
  onView,
  canManage = false,
  canEdit = false,
}: AgentCardProps) => (
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
          background: 'linear-gradient(135deg, #dbeafe 0%, #f9f0ff 100%)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <RobotOutlined style={{ fontSize: 18, color: '#2563eb' }} />
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

    <Text className="long-text" strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>
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
      className="responsive-card-actions"
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
            aria-label="执行 Agent"
            className="responsive-touch-target"
            type="text"
            size="small"
            icon={<PlayCircleOutlined />}
            onClick={() => onExecute(agent)}
            style={{ color: '#2563eb' }}
          />
        </Tooltip>
        {!canManage &&
          (canEdit ? (
            <Tooltip title="编辑">
              <Button
                aria-label="编辑 Agent"
                className="responsive-touch-target"
                type="text"
                size="small"
                icon={<EditOutlined />}
                onClick={() => onEdit(agent)}
              />
            </Tooltip>
          ) : (
            onView && (
              <Tooltip title="查看配置">
                <Button
                  aria-label="查看 Agent 配置"
                  className="responsive-touch-target"
                  type="text"
                  size="small"
                  icon={<EyeOutlined />}
                  onClick={() => onView(agent)}
                />
              </Tooltip>
            )
          ))}
        {canManage && (
          <>
            <Tooltip title="编辑">
              <Button
                aria-label="编辑 Agent"
                className="responsive-touch-target"
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
                <Button
                  aria-label="删除 Agent"
                  className="responsive-touch-target"
                  type="text"
                  size="small"
                  danger
                  icon={<DeleteOutlined />}
                />
              </Popconfirm>
            </Tooltip>
          </>
        )}
      </Space>
    </div>
  </Card>
);
