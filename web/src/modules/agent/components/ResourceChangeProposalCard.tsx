import { ArrowRightOutlined, ClockCircleOutlined } from '@ant-design/icons';
import { Button, Space, Tag, Typography } from 'antd';
import { Link } from 'react-router-dom';

import type { ResourceChangeProposalArtifact } from '../model/proposal';

const { Text } = Typography;
const KIND_LABEL = { agent: 'Agent', skill_draft: 'Skill 草稿', mcp_config: 'MCP 配置', knowledge_workspace: '知识库' };
const STATUS_LABEL: Record<string, string> = {
  ready_for_review: '待审阅', applied: '已应用', invalid: '无效', stale: '已冲突', expired: '已过期',
  failed: '应用失败', unknown_outcome: '结果未知', cancelled: '已取消', confirmed: '已确认', applying: '应用中', draft: '草稿',
};

export const ResourceChangeProposalCard = ({ proposal }: { proposal: ResourceChangeProposalArtifact }) => (
  <section
    aria-label="资源变更提案"
    style={{ marginTop: 10, borderInlineStart: '3px solid #2563eb', padding: '10px 12px', background: '#f7f9fc', minWidth: 0 }}
  >
    <Space direction="vertical" size={6} style={{ width: '100%', minWidth: 0 }}>
      <Space wrap size={6}>
        <Text strong>{KIND_LABEL[proposal.resourceKind]}</Text>
        <Tag color={proposal.operation === 'create' ? 'green' : 'blue'}>{proposal.operation === 'create' ? '新建' : '更新'}</Tag>
        <Tag>{STATUS_LABEL[proposal.status] || proposal.status}</Tag>
      </Space>
      <Text style={{ overflowWrap: 'anywhere' }}>{proposal.summary}</Text>
      <Space wrap style={{ justifyContent: 'space-between', width: '100%' }}>
        <Text type="secondary" style={{ fontSize: 12 }}><ClockCircleOutlined /> 有效期至 {new Date(proposal.expiresAt).toLocaleString('zh-CN')}</Text>
        <Link aria-label="审阅变更" to={`/resource-change-proposals/${proposal.id}`}>
          <Button type="link" size="small" icon={<ArrowRightOutlined />} iconPosition="end">审阅变更</Button>
        </Link>
      </Space>
    </Space>
  </section>
);
