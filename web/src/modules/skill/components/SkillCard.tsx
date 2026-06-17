import {
  CodeOutlined,
  DeleteOutlined,
  EditOutlined,
  GlobalOutlined,
  PartitionOutlined,
  RobotOutlined,
} from '@ant-design/icons';
import { Button, Card, Space, Tag, Tooltip, Typography } from 'antd';
import type { ReactNode } from 'react';

import type { Skill } from '../model/skill';

import { DangerPopconfirm } from '@/shared/ui';

const { Text, Paragraph } = Typography;

interface TypeMeta {
  color: string;
  bg: string;
  label: string;
  icon: ReactNode;
}

const TYPE_META: Record<string, TypeMeta> = {
  code: { color: '#52c41a', bg: '#f6ffed', label: 'Code', icon: <CodeOutlined /> },
  llm: { color: '#1677ff', bg: '#e6f4ff', label: 'LLM', icon: <RobotOutlined /> },
  http: { color: '#fa8c16', bg: '#fff7e6', label: 'HTTP', icon: <GlobalOutlined /> },
  default: { color: '#8c8c8c', bg: '#f5f5f5', label: '其他', icon: <PartitionOutlined /> },
};

const typeMeta = (t: string): TypeMeta => TYPE_META[t] || TYPE_META.default;

interface SkillCardProps {
  skill: Skill;
  onEdit: (id: string) => void;
  onDelete: (id: string) => void;
}

export const SkillCard = ({ skill, onEdit, onDelete }: SkillCardProps) => {
  const meta = typeMeta(skill.type);
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
            background: meta.bg,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
            fontSize: 18,
            color: meta.color,
          }}
        >
          {meta.icon}
        </div>
        <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <Tag
            style={{
              border: 'none',
              borderRadius: 6,
              fontSize: 11,
              background: meta.bg,
              color: meta.color,
              fontWeight: 500,
            }}
          >
            {meta.label}
          </Tag>
          {skill.type === 'code' && skill.config?.language && (
            <Tag
              style={{
                border: 'none',
                borderRadius: 6,
                fontSize: 10,
                background: '#f5f5f5',
                color: '#666',
                margin: 0,
              }}
            >
              {skill.config.language}
            </Tag>
          )}
        </div>
      </div>

      <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>
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
