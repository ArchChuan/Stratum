import { Background, Controls, ReactFlow } from '@xyflow/react';

import { createInitialEditorState, toFlowEdges, toFlowNodes } from '../model/editor';
import type { WorkflowRunState } from '../model/run-state';

import { WorkflowNodeCard } from './nodes/WorkflowNodeCard';

const nodeTypes = { workflowNode: WorkflowNodeCard };
const labels: Record<string, string> = { pending: '等待中', ready: '就绪', claimed: '已领取', running: '运行中', succeeded: '已完成', failed: '失败', retry_wait: '等待重试', skipped: '已跳过', paused: '已暂停', canceled: '已取消', manual_intervention: '等待人工处理' };

export const WorkflowRunCanvas = ({ state, selectedNodeId, onSelectNode }: { state: WorkflowRunState; selectedNodeId?: string; onSelectNode: (nodeId: string) => void }) => {
  const editor = createInitialEditorState(state.run.snapshot);
  const nodes = toFlowNodes(editor).map((node, index) => {
    const attempt = [...state.attempts].reverse().find((item) => item.node_id === node.id);
    return { ...node, position: { x: 100 + index * 280, y: 160 }, draggable: false, connectable: false, selected: selectedNodeId === node.id, data: { ...node.data, selected: selectedNodeId === node.id, statusLabel: labels[attempt?.status || 'pending'] } };
  });
  return <section aria-label="工作流运行图" className="workflow-readonly-canvas">
    <ReactFlow nodes={nodes} edges={toFlowEdges(editor)} nodeTypes={nodeTypes} nodesDraggable={false} nodesConnectable={false} onNodeClick={(_, node) => onSelectNode(node.id)} fitView>
      <Background gap={24} size={1} /><Controls showInteractive={false} />
    </ReactFlow>
  </section>;
};
