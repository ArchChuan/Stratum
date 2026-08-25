import { fireEvent, render, screen } from '@testing-library/react';
import { message } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { createInitialEditorState, workflowEditorReducer } from '../model/editor';

import { WorkflowCanvas } from './WorkflowCanvas';

const fitView = vi.hoisted(() => vi.fn());
// jsdom 的 DragEvent 不透传 clientX（探针已验证为 undefined），这里 mock 返回
// 固定值：只验证「drop → types 校验 → screenToFlowPosition → insert」链路。
const screenToFlowPosition = vi.hoisted(() => vi.fn(() => ({ x: 100, y: 120 })));
const connectHandler = vi.hoisted(() => ({ current: null as ((connection: any) => void) | null }));

vi.mock('@xyflow/react', () => ({
  ReactFlow: ({ nodes, nodeTypes, onInit, onConnect, onDrop, onDragOver }: any) => {
    onInit({ fitView, screenToFlowPosition });
    connectHandler.current = onConnect;
    const NodeComponent = nodeTypes.workflowNode;
    return <div data-testid="flow-container" onDrop={onDrop} onDragOver={onDragOver}>
      {nodes.map((node: any) => <NodeComponent key={node.id} data={node.data} selected={false} />)}
    </div>;
  },
  Background: () => null,
  Controls: () => null,
  MiniMap: () => null,
  Handle: () => null,
  Position: { Left: 'left', Right: 'right' },
  MarkerType: { ArrowClosed: 'arrowclosed' },
}));

describe('WorkflowCanvas', () => {
  it('inserts from the palette and exposes stable node dimensions', () => {
    const dispatch = vi.fn();
    const { container } = render(<WorkflowCanvas
      state={createInitialEditorState()}
      dispatch={dispatch}
      createNodeId={() => 'node-new'}
      createEdgeId={() => 'edge-new'}
    />);
    fireEvent.click(screen.getByRole('button', { name: '添加Agent节点' }));
    expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({ type: 'node.insert', nodeId: 'node-new', nodeType: 'agent' }));
    expect(container.querySelector('.workflow-node-size-contract')).toHaveAttribute('data-width', '224');
    expect(container.querySelector('.workflow-node-size-contract')).toHaveAttribute('data-height', '88');
  });

  it('renders accessible nodes, fits the viewport, and deletes the selection from the keyboard', () => {
    let state = workflowEditorReducer(createInitialEditorState(), {
      type: 'node.insert', nodeId: 'node-1', nodeType: 'approval', position: { x: 80, y: 80 },
    });
    state = workflowEditorReducer(state, { type: 'node.rename', nodeId: 'node-1', name: '主管确认' });
    const dispatch = vi.fn();
    render(<WorkflowCanvas state={state} dispatch={dispatch} createNodeId={() => 'n'} createEdgeId={() => 'e'} />);
    expect(screen.getByRole('article', { name: '主管确认节点' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '适应画布' }));
    expect(fitView).toHaveBeenCalled();
    fireEvent.keyDown(screen.getByRole('region', { name: '工作流画布' }), { key: 'Delete' });
    expect(dispatch).toHaveBeenCalledWith({ type: 'node.delete', nodeId: 'node-1' });
  });

  it('drops a palette node at the pointer position in flow coordinates', () => {
    const dispatch = vi.fn();
    render(<WorkflowCanvas
      state={createInitialEditorState()}
      dispatch={dispatch}
      createNodeId={() => 'node-drop'}
      createEdgeId={() => 'e'}
    />);
    fireEvent.drop(screen.getByTestId('flow-container'), {
      dataTransfer: { types: ['application/x-workflow-node-type'], getData: () => 'agent' },
    });
    expect(screenToFlowPosition).toHaveBeenCalled();
    expect(dispatch).toHaveBeenCalledWith({ type: 'node.insert', nodeId: 'node-drop', nodeType: 'agent', position: { x: 100, y: 120 } });
  });

  it('ignores drops that do not carry the palette drag type', () => {
    const dispatch = vi.fn();
    render(<WorkflowCanvas
      state={createInitialEditorState()}
      dispatch={dispatch}
      createNodeId={() => 'node-x'}
      createEdgeId={() => 'e'}
    />);
    fireEvent.drop(screen.getByTestId('flow-container'), {
      clientX: 400,
      clientY: 300,
      dataTransfer: { types: ['text/plain'], getData: () => 'agent' },
    });
    expect(dispatch).not.toHaveBeenCalled();
  });

  it('maps condition branch handles to edge fields when connecting', () => {
    const dispatch = vi.fn();
    render(<WorkflowCanvas
      state={createInitialEditorState()}
      dispatch={dispatch}
      createNodeId={() => 'n'}
      createEdgeId={() => 'edge-yes'}
    />);
    connectHandler.current?.({ source: 'node-condition', target: 'node-1', sourceHandle: 'yes' });
    connectHandler.current?.({ source: 'node-condition', target: 'node-2', sourceHandle: 'no' });
    connectHandler.current?.({ source: 'node-condition', target: 'node-3', sourceHandle: 'default' });
    connectHandler.current?.({ source: 'node-agent', target: 'node-4' });
    expect(dispatch).toHaveBeenNthCalledWith(1, { type: 'edge.connect', edgeId: 'edge-yes', from: 'node-condition', to: 'node-1', conditionValue: true });
    expect(dispatch).toHaveBeenNthCalledWith(2, { type: 'edge.connect', edgeId: 'edge-yes', from: 'node-condition', to: 'node-2', conditionValue: false });
    expect(dispatch).toHaveBeenNthCalledWith(3, { type: 'edge.connect', edgeId: 'edge-yes', from: 'node-condition', to: 'node-3', isDefault: true });
    expect(dispatch).toHaveBeenNthCalledWith(4, { type: 'edge.connect', edgeId: 'edge-yes', from: 'node-agent', to: 'node-4' });
  });

  it('blocks a connection that would form a cycle and shows an error', () => {
    let state = workflowEditorReducer(createInitialEditorState(), {
      type: 'node.insert', nodeId: 'node-a', nodeType: 'agent', position: { x: 80, y: 80 },
    });
    state = workflowEditorReducer(state, {
      type: 'node.insert', nodeId: 'node-b', nodeType: 'skill', position: { x: 320, y: 80 },
    });
    state = workflowEditorReducer(state, {
      type: 'edge.connect', edgeId: 'edge-1', from: 'node-a', to: 'node-b',
    });
    const messageError = vi.spyOn(message, 'error').mockImplementation((() => undefined) as unknown as typeof message.error);
    const dispatch = vi.fn();
    render(<WorkflowCanvas state={state} dispatch={dispatch} createNodeId={() => 'n'} createEdgeId={() => 'edge-2'} />);
    // 反向连边 b→a 会成环：前端连线守卫提示并阻止，不 dispatch。
    connectHandler.current?.({ source: 'node-b', target: 'node-a' });
    expect(messageError).toHaveBeenCalledWith({ content: '连线会形成环，请选择其他节点', duration: 3 });
    expect(dispatch).not.toHaveBeenCalled();
    messageError.mockRestore();
  });

  it('deletes a node from the card delete button', () => {
    const state = workflowEditorReducer(createInitialEditorState(), {
      type: 'node.insert', nodeId: 'node-mcp', nodeType: 'mcp_tool', position: { x: 80, y: 80 },
    });
    const dispatch = vi.fn();
    render(<WorkflowCanvas state={state} dispatch={dispatch} createNodeId={() => 'n'} createEdgeId={() => 'e'} />);
    fireEvent.click(screen.getByRole('button', { name: '删除节点' }));
    expect(dispatch).toHaveBeenCalledWith({ type: 'node.delete', nodeId: 'node-mcp' });
  });
});
