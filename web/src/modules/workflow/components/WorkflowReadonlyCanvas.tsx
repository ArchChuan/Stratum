import { Background, Controls, ReactFlow } from '@xyflow/react';
import { useState } from 'react';

import { createInitialEditorState, toFlowEdges, toFlowNodes } from '../model/editor';
import type { WorkflowSpec } from '../model/workflow';

import { WorkflowNodeReadonlyDetail } from './WorkflowNodeReadonlyDetail';
import { WorkflowNodeCard } from './nodes/WorkflowNodeCard';

const nodeTypes = { workflowNode: WorkflowNodeCard };

// WorkflowReadonlyCanvas 是工作流详情页/版本页共用的只读画布：节点可点击选中，
// 右侧展示选中节点的只读配置详情（WorkflowNodeReadonlyDetail）。
// skillRevisionLabels 由调用方（详情页/版本页）从技能资源构建，用于把 skill 节点的
// skill_revision_id 翻译成可读版本名。
export const WorkflowReadonlyCanvas = ({ spec, skillRevisionLabels }: {
  spec: WorkflowSpec;
  skillRevisionLabels?: Record<string, string>;
}) => {
  const state = createInitialEditorState(spec);
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  const selectedNode = state.spec.nodes.find((node) => node.id === selectedNodeId);
  return <section aria-label="工作流版本图" className="workflow-readonly-layout">
    <div className="workflow-readonly-canvas">
      <ReactFlow
        nodes={toFlowNodes(state).map((node, index) => ({
          ...node,
          position: node.position.x || node.position.y ? node.position : { x: 120 + index * 280, y: 160 },
          draggable: false,
          connectable: false,
          selected: selectedNodeId === node.id,
          data: { ...node.data, selected: selectedNodeId === node.id },
        }))}
        edges={toFlowEdges(state).map((edge) => ({ ...edge, animated: false, selectable: false }))}
        nodeTypes={nodeTypes}
        nodesDraggable={false}
        nodesConnectable={false}
        onNodeClick={(_, node) => setSelectedNodeId(node.id)}
        onPaneClick={() => setSelectedNodeId(undefined)}
        nodeOrigin={[0.5, 0.5]}
        fitView
      >
        <Background gap={24} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
    <WorkflowNodeReadonlyDetail node={selectedNode} skillRevisionLabels={skillRevisionLabels} />
  </section>;
};
