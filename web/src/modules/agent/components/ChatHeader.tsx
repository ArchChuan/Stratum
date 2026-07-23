import { BranchesOutlined, MenuOutlined, RobotOutlined, SettingOutlined } from '@ant-design/icons';
import { Button, Popover, Select, Space, Tag, Typography, message } from 'antd';
import { useState } from 'react';
import { useInRouterContext, useNavigate } from 'react-router-dom';

import type { Agent } from '../model/agent';

import { workflowApi } from '@/modules/workflow/api/workflow.api';

const { Text } = Typography;

interface Props {
  agent?: Agent;
  isMobile?: boolean;
  onOpenConversations?: () => void;
  isAdmin?: boolean;
  onOpenSettings?: () => void;
}

const WorkflowShortcut = ({ isMobile }: { isMobile: boolean }) => {
  const navigate = useNavigate();
  const [workflowOpen, setWorkflowOpen] = useState(false);
  const [workflowLoading, setWorkflowLoading] = useState(false);
  const [workflows, setWorkflows] = useState<Array<{ value: string; label: string }>>([]);
  const openWorkflows = async (open: boolean) => {
    setWorkflowOpen(open);
    if (!open || workflows.length) return;
    setWorkflowLoading(true);
    try {
      const result = await workflowApi.listWorkflows({ page: 1, pageSize: 50 });
      setWorkflows(result.workflows.map((workflow) => ({ value: workflow.id, label: workflow.name })));
    } catch (error: unknown) {
      message.error({ content: (error as { response?: { data?: { error?: string } } }).response?.data?.error || '操作失败', duration: 0 });
    } finally {
      setWorkflowLoading(false);
    }
  };
  return <Popover
    open={workflowOpen}
    onOpenChange={openWorkflows}
    trigger="click"
    content={<Select
      aria-label="选择固定工作流"
      showSearch
      loading={workflowLoading}
      placeholder="搜索工作流"
      options={workflows}
      optionFilterProp="label"
      style={{ width: isMobile ? 240 : 300 }}
      onChange={(workflowId) => { setWorkflowOpen(false); navigate(`/workflows/${workflowId}/run`); }}
    />}
  >
    <Button icon={<BranchesOutlined />}>运行固定工作流</Button>
  </Popover>;
};

export const ChatHeader = ({
  agent,
  isMobile = false,
  onOpenConversations,
  isAdmin = false,
  onOpenSettings,
}: Props) => {
  const inRouter = useInRouterContext();
  return (
  <div
    style={{
      minHeight: 48,
      background: '#fff',
      borderBottom: '1px solid #f0f0f0',
      display: 'flex',
      alignItems: 'center',
      padding: isMobile ? '0 12px' : '0 20px',
      gap: 8,
      flexWrap: 'wrap',
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
    <Space size={6} wrap style={{ minWidth: 0 }}>
      <Text strong style={{ fontSize: 15 }}>{agent?.name || '请选择 Agent'}</Text>
      {agent?.isSystem && (
        <Tag color="blue" bordered={false} style={{ marginInlineEnd: 0, fontSize: 10 }}>
          系统内置
        </Tag>
      )}
    </Space>
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
    {agent?.isSystem && !agent.llmModel && !isAdmin && (
      <Text type="secondary" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>
        尚未配置模型，请联系租户管理员
      </Text>
    )}
    <Space size={8} wrap style={{ marginInlineStart: 'auto' }}>
      {agent?.isSystem && isAdmin && onOpenSettings && (
        <Button
          size="small"
          icon={<SettingOutlined />}
          aria-label="设置助手模型"
          onClick={onOpenSettings}
        >
          {isMobile ? null : '助手设置'}
        </Button>
      )}
      {inRouter && <WorkflowShortcut isMobile={isMobile} />}
    </Space>
  </div>
  );
};
