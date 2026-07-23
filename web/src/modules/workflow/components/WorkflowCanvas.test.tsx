import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { createInitialEditorState, workflowEditorReducer } from '../model/editor';

import { WorkflowCanvas } from './WorkflowCanvas';

const fitView = vi.hoisted(() => vi.fn());

vi.mock('@xyflow/react', () => ({
  ReactFlow: ({ nodes, nodeTypes, onInit }: any) => {
    onInit({ fitView });
    const NodeComponent = nodeTypes.workflowNode;
    return <div>{nodes.map((node: any) => <NodeComponent key={node.id} data={node.data} selected={false} />)}</div>;
  },
  Background: () => null,
  Controls: () => null,
  MiniMap: () => null,
  Handle: () => null,
  Position: { Left: 'left', Right: 'right' },
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
});
