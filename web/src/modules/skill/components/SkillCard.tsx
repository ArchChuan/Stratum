import { DeleteOutlined, EditOutlined, ToolOutlined } from '@ant-design/icons';
import { Button, Card, Space, Tooltip, Typography } from 'antd';

import type { Skill } from '../model/skill';

import { DangerPopconfirm } from '@/shared/ui';

const { Text, Paragraph } = Typography;

interface SkillCardProps {
  skill: Skill;
  onEdit: (id: string) => void;
  onDelete: (id: string) => void;
}

export const SkillCard = ({ skill, onEdit, onDelete }: SkillCardProps) => {
  return (
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
            background: '#f0f5ff',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
            fontSize: 18,
            color: '#2f54eb',
          }}
        >
          <ToolOutlined />
        </div>
      </div>

      <Text className="long-text" strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>
        {skill.name}
      </Text>
      <Paragraph
        type="secondary"
        ellipsis={{ rows: 2 }}
        style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
      >
        {skill.description || '暂无描述'}
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
        <Text type="secondary" style={{ fontSize: 12 }}>
          {skill.created_at ? new Date(skill.created_at).toLocaleDateString('zh-CN') : '-'}
        </Text>
        <Space size={0}>
          <Tooltip title="编辑技能">
            <Button
              type="text"
              size="small"
              icon={<EditOutlined />}
              onClick={() => onEdit(skill.id)}
            />
          </Tooltip>
          <DangerPopconfirm
            title={`确定删除技能 "${skill.name}" 吗？`}
            okText="删除"
            onConfirm={() => onDelete(skill.id)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </DangerPopconfirm>
        </Space>
      </div>
    </Card>
  );
};
