import { AuditOutlined, BranchesOutlined, CloseOutlined, RobotOutlined, SafetyCertificateOutlined, ToolOutlined } from '@ant-design/icons';
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

const branchLabels: Array<{ id: string; label: string }> = [
  { id: 'yes', label: '是' },
  { id: 'no', label: '否' },
  { id: 'default', label: '默认' },
];

export const WorkflowNodeCard = ({ data, selected }: NodeProps<WorkflowFlowNode>) => {
  const presentation = presentations[data.node.type];
  return <article
    aria-label={`${data.node.name || presentation.label}节点`}
    className={`workflow-node-card${selected || data.selected ? ' is-selected' : ''}`}
  >
    <Handle type="target" position={Position.Left} />
    {data.onDelete && <button
      aria-label="删除节点"
      className="workflow-node-delete"
      // 双重阻断：pointerdown 阻止 React Flow 拖拽拾取，click 阻止选中冒泡。
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => { event.stopPropagation(); data.onDelete?.(data.node.id); }}
    ><CloseOutlined /></button>}
    <span className={`workflow-node-icon type-${data.node.type}`}>{presentation.icon}</span>
    <span><strong>{data.node.name || presentation.label}</strong><small>{data.statusLabel || presentation.label}</small></span>
    {data.node.type === 'condition'
      ? <span className="workflow-node-branches">
          {branchLabels.map((branch) => <span className="workflow-node-branch" key={branch.id}>
            <span className="workflow-branch-label">{branch.label}</span>
            <Handle type="source" position={Position.Right} id={branch.id} className="workflow-handle-branch" />
          </span>)}
        </span>
      : <Handle type="source" position={Position.Right} />}
  </article>;
};
