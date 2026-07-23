import { BranchesOutlined, MenuOutlined, RobotOutlined } from '@ant-design/icons';
import { Button, Popover, Select, Tag, Typography, message } from 'antd';
import { useState } from 'react';
import { useInRouterContext, useNavigate } from 'react-router-dom';

import type { Agent } from '../model/agent';

import { workflowApi } from '@/modules/workflow/api/workflow.api';

const { Text } = Typography;

interface Props {
  agent?: Agent;
  isMobile?: boolean;
  onOpenConversations?: () => void;
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

export const ChatHeader = ({ agent, isMobile = false, onOpenConversations }: Props) => {
  const inRouter = useInRouterContext();
  return (
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
    <span style={{ marginLeft: 'auto' }}>
      {inRouter && <WorkflowShortcut isMobile={isMobile} />}
    </span>
  </div>
  );
};
