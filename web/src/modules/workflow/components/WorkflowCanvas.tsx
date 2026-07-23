import { AimOutlined } from '@ant-design/icons';
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  type Connection,
  type Edge,
  type ReactFlowInstance,
} from '@xyflow/react';
import { Button } from 'antd';
import { useMemo, useRef } from 'react';

import { toFlowEdges, toFlowNodes, type WorkflowEditorAction, type WorkflowEditorState, type WorkflowFlowNode } from '../model/editor';
import type { WorkflowNodeType } from '../model/workflow';

import { WorkflowNodePalette } from './WorkflowNodePalette';
import { WorkflowNodeCard } from './nodes/WorkflowNodeCard';

import { WORKFLOW_NODE_HEIGHT, WORKFLOW_NODE_WIDTH } from '@/constants';

const nodeTypes = { workflowNode: WorkflowNodeCard };

export const WorkflowCanvas = ({
  state,
  dispatch,
  createNodeId,
  createEdgeId,
}: {
  state: WorkflowEditorState;
  dispatch: React.Dispatch<WorkflowEditorAction>;
  createNodeId: () => string;
  createEdgeId: () => string;
}) => {
  const instanceRef = useRef<ReactFlowInstance<WorkflowFlowNode, Edge> | null>(null);
  const nodes = useMemo(() => toFlowNodes(state), [state]);
  const edges = useMemo(() => toFlowEdges(state), [state]);
  const insertNode = (nodeType: WorkflowNodeType) => dispatch({
    type: 'node.insert', nodeId: createNodeId(), nodeType, position: { x: 80 + nodes.length * 32, y: 80 + nodes.length * 24 },
  });
  const connect = (connection: Connection) => {
    if (!connection.source || !connection.target) return;
    dispatch({ type: 'edge.connect', edgeId: createEdgeId(), from: connection.source, to: connection.target });
  };
  const deleteSelection = () => {
    if (!state.selected) return;
    dispatch(state.selected.kind === 'node'
      ? { type: 'node.delete', nodeId: state.selected.id }
      : { type: 'edge.delete', edgeId: state.selected.id });
  };

  return <div className="workflow-editor-workspace">
    <WorkflowNodePalette onInsert={insertNode} />
    <section
      aria-label="工作流画布"
      className="workflow-canvas"
      tabIndex={0}
      onKeyDown={(event) => { if (event.key === 'Delete' || event.key === 'Backspace') deleteSelection(); }}
    >
      <Button
        aria-label="适应画布"
        className="workflow-fit-view"
        icon={<AimOutlined />}
        onClick={() => instanceRef.current?.fitView()}
      >适应画布</Button>
      <ReactFlow<WorkflowFlowNode, Edge>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onInit={(instance) => { instanceRef.current = instance; }}
        onConnect={connect}
        onNodeClick={(_, node) => dispatch({ type: 'selection.set', selection: { kind: 'node', id: node.id } })}
        onEdgeClick={(_, edge) => dispatch({ type: 'selection.set', selection: { kind: 'edge', id: edge.id } })}
        onNodeDragStop={(_, node) => dispatch({ type: 'node.move', nodeId: node.id, position: node.position })}
        nodeOrigin={[0.5, 0.5]}
        defaultEdgeOptions={{ animated: true }}
        fitView
      >
        <Background gap={24} size={1} />
        <MiniMap nodeStrokeWidth={3} pannable zoomable />
        <Controls showInteractive={false} />
      </ReactFlow>
      <span className="workflow-node-size-contract" data-width={WORKFLOW_NODE_WIDTH} data-height={WORKFLOW_NODE_HEIGHT} />
    </section>
  </div>;
};
