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
import { Button, message } from 'antd';
import { useMemo, useRef } from 'react';

import { toFlowEdges, toFlowNodes, type WorkflowEditorAction, type WorkflowEditorState, type WorkflowFlowNode } from '../model/editor';
import { hasCycle } from '../model/graph';
import type { WorkflowEdge, WorkflowNodeType, WorkflowSpec } from '../model/workflow';

import { WORKFLOW_DRAG_TYPE, WorkflowNodePalette } from './WorkflowNodePalette';
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
  const canvasRef = useRef<HTMLElement | null>(null);
  const nodes = useMemo(
    () => toFlowNodes(state, (nodeId) => dispatch({ type: 'node.delete', nodeId })),
    [dispatch, state],
  );
  const edges = useMemo(() => toFlowEdges(state), [state]);
  const insertNode = (nodeType: WorkflowNodeType) => dispatch({
    type: 'node.insert', nodeId: createNodeId(), nodeType, position: { x: 80 + nodes.length * 32, y: 80 + nodes.length * 24 },
  });
  const connect = (connection: Connection) => {
    if (!connection.source || !connection.target) return;
    // 条件节点的分支 handle（是/否/默认）映射到边字段，非 condition 边无 sourceHandle
    const branch = connection.sourceHandle === 'yes'
      ? { conditionValue: true }
      : connection.sourceHandle === 'no'
        ? { conditionValue: false }
        : connection.sourceHandle === 'default' ? { isDefault: true } : {};
    const edgeId = createEdgeId();
    // 连线前用最新 spec 构造候选做环检测，成环则提示并阻止（后端保存同样 fail-closed）。
    // 候选边必须与 reducer 的 edge.connect 转换逻辑对齐（condition_value/default snake_case），
    // 否则环检测与落库图不一致，成环判定失真。
    const candidateEdge: WorkflowEdge = {
      id: edgeId,
      from: connection.source,
      to: connection.target,
      condition_value: connection.sourceHandle === 'yes' ? true : connection.sourceHandle === 'no' ? false : undefined,
      default: connection.sourceHandle === 'default',
    };
    const candidate: WorkflowSpec = {
      ...state.spec,
      edges: [...state.spec.edges, candidateEdge],
    };
    if (hasCycle(candidate)) {
      message.error({ content: '连线会形成环，请选择其他节点', duration: 3 });
      return;
    }
    dispatch({ type: 'edge.connect', edgeId, from: connection.source, to: connection.target, ...branch });
  };
  const deleteSelection = () => {
    if (!state.selected) return;
    dispatch(state.selected.kind === 'node'
      ? { type: 'node.delete', nodeId: state.selected.id }
      : { type: 'edge.delete', edgeId: state.selected.id });
  };
  const onDrop = (event: React.DragEvent) => {
    // 只接受本页面 palette 拖出的自定义类型，忽略外部拖拽内容
    if (!event.dataTransfer.types.includes(WORKFLOW_DRAG_TYPE)) return;
    event.preventDefault();
    const nodeType = event.dataTransfer.getData(WORKFLOW_DRAG_TYPE) as WorkflowNodeType;
    const position = instanceRef.current?.screenToFlowPosition({ x: event.clientX, y: event.clientY }) ?? { x: 0, y: 0 };
    dispatch({ type: 'node.insert', nodeId: createNodeId(), nodeType, position });
  };

  return <div className="workflow-editor-workspace">
    <WorkflowNodePalette onInsert={insertNode} />
    <section
      ref={canvasRef}
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
        onNodeClick={(_, node) => {
          // 点击节点后聚焦画布容器，保证依赖容器焦点的键盘 Delete 删除仍可用。
          canvasRef.current?.focus();
          dispatch({ type: 'selection.set', selection: { kind: 'node', id: node.id } });
        }}
        onEdgeClick={(_, edge) => dispatch({ type: 'selection.set', selection: { kind: 'edge', id: edge.id } })}
        onNodeDragStop={(_, node) => dispatch({ type: 'node.move', nodeId: node.id, position: node.position })}
        onDrop={onDrop}
        onDragOver={(event) => {
          if (event.dataTransfer.types.includes(WORKFLOW_DRAG_TYPE)) {
            event.preventDefault();
            event.dataTransfer.dropEffect = 'copy';
          }
        }}
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
