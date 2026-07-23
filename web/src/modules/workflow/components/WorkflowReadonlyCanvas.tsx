import { Background, Controls, ReactFlow } from '@xyflow/react';

import { createInitialEditorState, toFlowEdges, toFlowNodes } from '../model/editor';
import type { WorkflowSpec } from '../model/workflow';

import { WorkflowNodeCard } from './nodes/WorkflowNodeCard';

const nodeTypes = { workflowNode: WorkflowNodeCard };

export const WorkflowReadonlyCanvas = ({ spec }: { spec: WorkflowSpec }) => {
  const state = createInitialEditorState(spec);
  return <section aria-label="工作流版本图" className="workflow-readonly-canvas">
    <ReactFlow
      nodes={toFlowNodes(state).map((node, index) => ({
        ...node,
        position: node.position.x || node.position.y ? node.position : { x: 120 + index * 280, y: 160 },
        draggable: false,
        connectable: false,
        selectable: false,
      }))}
      edges={toFlowEdges(state).map((edge) => ({ ...edge, animated: false, selectable: false }))}
      nodeTypes={nodeTypes}
      nodesDraggable={false}
      nodesConnectable={false}
      elementsSelectable={false}
      fitView
    >
      <Background gap={24} size={1} />
      <Controls showInteractive={false} />
    </ReactFlow>
  </section>;
};
