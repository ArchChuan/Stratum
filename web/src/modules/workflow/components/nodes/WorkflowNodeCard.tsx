import { AuditOutlined, BranchesOutlined, RobotOutlined, SafetyCertificateOutlined, ToolOutlined } from '@ant-design/icons';
import { Handle, Position, type NodeProps } from '@xyflow/react';

import type { WorkflowFlowNode } from '../../model/editor';
import type { WorkflowNodeType } from '../../model/workflow';

const presentations: Record<WorkflowNodeType, { label: string; icon: React.ReactNode }> = {
  agent: { label: 'Agent', icon: <RobotOutlined /> },
  skill: { label: 'Skill', icon: <AuditOutlined /> },
  mcp_tool: { label: 'MCP 工具', icon: <ToolOutlined /> },
  condition: { label: '条件判断', icon: <BranchesOutlined /> },
  approval: { label: '人工审批', icon: <SafetyCertificateOutlined /> },
};

export const WorkflowNodeCard = ({ data, selected }: NodeProps<WorkflowFlowNode>) => {
  const presentation = presentations[data.node.type];
  return <article
    aria-label={`${data.node.name || presentation.label}节点`}
    className={`workflow-node-card${selected || data.selected ? ' is-selected' : ''}`}
  >
    <Handle type="target" position={Position.Left} />
    <span className={`workflow-node-icon type-${data.node.type}`}>{presentation.icon}</span>
    <span><strong>{data.node.name || presentation.label}</strong><small>{data.statusLabel || presentation.label}</small></span>
    <Handle type="source" position={Position.Right} />
  </article>;
};
