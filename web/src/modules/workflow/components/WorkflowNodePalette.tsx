import { AuditOutlined, BranchesOutlined, RobotOutlined, SafetyCertificateOutlined, ToolOutlined } from '@ant-design/icons';
import { Button, Typography } from 'antd';

import type { WorkflowNodeType } from '../model/workflow';

const { Text } = Typography;

/** dataTransfer 类型：仅本页面自定义类型，避免接收外部拖拽内容 */
export const WORKFLOW_DRAG_TYPE = 'application/x-workflow-node-type';

const nodeTypes: Array<{ type: WorkflowNodeType; label: string; icon: React.ReactNode }> = [
  { type: 'agent', label: 'Agent', icon: <RobotOutlined /> },
  { type: 'skill', label: 'Skill', icon: <AuditOutlined /> },
  { type: 'mcp_tool', label: 'MCP 工具', icon: <ToolOutlined /> },
  { type: 'condition', label: '条件判断', icon: <BranchesOutlined /> },
  { type: 'approval', label: '人工审批', icon: <SafetyCertificateOutlined /> },
];

export const WorkflowNodePalette = ({ onInsert }: { onInsert: (type: WorkflowNodeType) => void }) => (
  <aside className="workflow-node-palette" aria-label="节点工具箱">
    <Text strong>添加步骤</Text>
    <div className="workflow-node-palette-list">
      {nodeTypes.map((item) => (
        <Button
          draggable
          key={item.type}
          aria-label={`添加${item.label}节点`}
          icon={item.icon}
          onClick={() => onInsert(item.type)}
          onDragStart={(event) => {
            event.dataTransfer.setData(WORKFLOW_DRAG_TYPE, item.type);
            event.dataTransfer.effectAllowed = 'move';
          }}
        >
          {item.label}
        </Button>
      ))}
    </div>
  </aside>
);
