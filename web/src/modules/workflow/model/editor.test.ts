import { describe, expect, it } from 'vitest';

import {
  createInitialEditorState,
  toFlowEdges,
  toFlowNodes,
  workflowEditorReducer,
} from './editor';

const insertAgent = {
  type: 'node.insert' as const,
  nodeId: 'node-agent',
  nodeType: 'agent' as const,
  position: { x: 80, y: 120 },
};

describe('workflow editor reducer', () => {
  it('inserts, selects, renames, connects, and labels condition edges', () => {
    let state = createInitialEditorState();
    state = workflowEditorReducer(state, insertAgent);
    state = workflowEditorReducer(state, {
      type: 'node.insert', nodeId: 'node-condition', nodeType: 'condition', position: { x: 360, y: 120 },
    });
    state = workflowEditorReducer(state, { type: 'node.rename', nodeId: 'node-agent', name: '资料整理' });
    state = workflowEditorReducer(state, {
      type: 'edge.connect', edgeId: 'edge-yes', from: 'node-condition', to: 'node-agent', conditionValue: true,
    });

    expect(state.spec.nodes).toHaveLength(2);
    expect(state.spec.nodes[0].name).toBe('资料整理');
    expect(state.spec.edges[0]).toMatchObject({ id: 'edge-yes', condition_value: true });
    expect(state.selected).toEqual({ kind: 'edge', id: 'edge-yes' });
    expect(state.dirty).toBe(true);
  });

  it('rejects self-connections and deletes attached edges with a node', () => {
    let state = workflowEditorReducer(createInitialEditorState(), insertAgent);
    state = workflowEditorReducer(state, {
      type: 'edge.connect', edgeId: 'self', from: 'node-agent', to: 'node-agent',
    });
    expect(state.spec.edges).toHaveLength(0);

    state = workflowEditorReducer(state, {
      type: 'node.insert', nodeId: 'node-skill', nodeType: 'skill', position: { x: 360, y: 120 },
    });
    state = workflowEditorReducer(state, {
      type: 'edge.connect', edgeId: 'edge-1', from: 'node-agent', to: 'node-skill',
    });
    state = workflowEditorReducer(state, { type: 'node.delete', nodeId: 'node-agent' });
    expect(state.spec.nodes.map((node) => node.id)).toEqual(['node-skill']);
    expect(state.spec.edges).toHaveLength(0);
  });

  it('resets from the server without marking the editor dirty', () => {
    const dirty = workflowEditorReducer(createInitialEditorState(), insertAgent);
    const reset = workflowEditorReducer(dirty, {
      type: 'server.reset',
      spec: dirty.spec,
      positions: { 'node-agent': { x: 20, y: 40 } },
    });
    expect(reset.dirty).toBe(false);
    expect(reset.selected).toBeNull();
    expect(reset.positions['node-agent']).toEqual({ x: 20, y: 40 });
  });

  it('converts domain graph state to deterministic React Flow elements', () => {
    let state = workflowEditorReducer(createInitialEditorState(), insertAgent);
    state = workflowEditorReducer(state, {
      type: 'node.insert', nodeId: 'node-approval', nodeType: 'approval', position: { x: 360, y: 120 },
    });
    state = workflowEditorReducer(state, {
      type: 'edge.connect', edgeId: 'edge-1', from: 'node-agent', to: 'node-approval', isDefault: true,
    });

    expect(toFlowNodes(state)).toEqual([
      expect.objectContaining({ id: 'node-agent', type: 'workflowNode', position: { x: 80, y: 120 } }),
      expect.objectContaining({ id: 'node-approval', type: 'workflowNode', position: { x: 360, y: 120 } }),
    ]);
    expect(toFlowEdges(state)).toEqual([
      expect.objectContaining({ id: 'edge-1', source: 'node-agent', target: 'node-approval', label: '默认' }),
    ]);
  });
});
