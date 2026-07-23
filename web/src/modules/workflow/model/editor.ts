import type { Edge, Node, XYPosition } from '@xyflow/react';

import type { WorkflowEdge, WorkflowNode, WorkflowNodeType, WorkflowSpec } from './workflow';

export type EditorSelection = { kind: 'node' | 'edge'; id: string } | null;

export interface WorkflowEditorState {
  spec: WorkflowSpec;
  positions: Record<string, XYPosition>;
  selected: EditorSelection;
  dirty: boolean;
}

export type WorkflowEditorAction =
  | { type: 'server.reset'; spec: WorkflowSpec; positions?: Record<string, XYPosition> }
  | { type: 'node.insert'; nodeId: string; nodeType: WorkflowNodeType; position: XYPosition }
  | { type: 'node.move'; nodeId: string; position: XYPosition }
  | { type: 'node.rename'; nodeId: string; name: string }
  | { type: 'node.update'; node: WorkflowNode }
  | { type: 'node.delete'; nodeId: string }
  | { type: 'edge.connect'; edgeId: string; from: string; to: string; conditionValue?: boolean; isDefault?: boolean }
  | { type: 'edge.delete'; edgeId: string }
  | { type: 'selection.set'; selection: EditorSelection };

export interface WorkflowNodeData extends Record<string, unknown> {
  node: WorkflowNode;
  selected: boolean;
  statusLabel?: string;
}

const emptySpec = (): WorkflowSpec => ({ nodes: [], edges: [], max_concurrency: 0 });

const createNode = (id: string, type: WorkflowNodeType): WorkflowNode => {
  const base = {
    id,
    name: '',
    input_mapping: {},
    output_mapping: {},
    retry: { max_attempts: 0, backoff_ms: 0 },
    timeout_ms: 0,
  };
  switch (type) {
    case 'agent': return { ...base, type, agent_id: '' };
    case 'skill': return { ...base, type, agent_id: '', skill_id: '', skill_revision_id: '' };
    case 'mcp_tool': return {
      ...base, type, agent_id: '', mcp_server_id: '', mcp_tool_name: '', effect_class: 'pure',
    };
    case 'condition': return { ...base, type, agent_id: '', condition: '' };
    case 'approval': return { ...base, type, agent_id: '' };
  }
};

export const createInitialEditorState = (spec: WorkflowSpec = emptySpec()): WorkflowEditorState => ({
  spec,
  positions: {},
  selected: null,
  dirty: false,
});

export const workflowEditorReducer = (
  state: WorkflowEditorState,
  action: WorkflowEditorAction,
): WorkflowEditorState => {
  switch (action.type) {
    case 'server.reset':
      return { spec: action.spec, positions: action.positions || {}, selected: null, dirty: false };
    case 'node.insert':
      if (state.spec.nodes.some((node) => node.id === action.nodeId)) return state;
      return {
        ...state,
        spec: { ...state.spec, nodes: [...state.spec.nodes, createNode(action.nodeId, action.nodeType)] },
        positions: { ...state.positions, [action.nodeId]: action.position },
        selected: { kind: 'node', id: action.nodeId },
        dirty: true,
      };
    case 'node.move':
      return { ...state, positions: { ...state.positions, [action.nodeId]: action.position }, dirty: true };
    case 'node.rename':
      return {
        ...state,
        spec: {
          ...state.spec,
          nodes: state.spec.nodes.map((node) => node.id === action.nodeId ? { ...node, name: action.name } : node),
        },
        dirty: true,
      };
    case 'node.update':
      return {
        ...state,
        spec: {
          ...state.spec,
          nodes: state.spec.nodes.map((node) => node.id === action.node.id ? action.node : node),
        },
        dirty: true,
      };
    case 'node.delete':
      return {
        ...state,
        spec: {
          ...state.spec,
          nodes: state.spec.nodes.filter((node) => node.id !== action.nodeId),
          edges: state.spec.edges.filter((edge) => edge.from !== action.nodeId && edge.to !== action.nodeId),
        },
        positions: Object.fromEntries(Object.entries(state.positions).filter(([id]) => id !== action.nodeId)),
        selected: state.selected?.id === action.nodeId ? null : state.selected,
        dirty: true,
      };
    case 'edge.connect': {
      if (action.from === action.to || state.spec.edges.some((edge) => edge.id === action.edgeId)) return state;
      const edge: WorkflowEdge = {
        id: action.edgeId,
        from: action.from,
        to: action.to,
        condition_value: action.conditionValue,
        default: action.isDefault || false,
      };
      return {
        ...state,
        spec: { ...state.spec, edges: [...state.spec.edges, edge] },
        selected: { kind: 'edge', id: action.edgeId },
        dirty: true,
      };
    }
    case 'edge.delete':
      return {
        ...state,
        spec: { ...state.spec, edges: state.spec.edges.filter((edge) => edge.id !== action.edgeId) },
        selected: state.selected?.id === action.edgeId ? null : state.selected,
        dirty: true,
      };
    case 'selection.set':
      return { ...state, selected: action.selection };
  }
};

export const toFlowNodes = (state: WorkflowEditorState): Node<WorkflowNodeData>[] => state.spec.nodes.map((node) => ({
  id: node.id,
  type: 'workflowNode',
  position: state.positions[node.id] || { x: 0, y: 0 },
  data: { node, selected: state.selected?.kind === 'node' && state.selected.id === node.id },
}));

export const toFlowEdges = (state: WorkflowEditorState): Edge[] => state.spec.edges.map((edge) => ({
  id: edge.id || `${edge.from}-${edge.to}`,
  source: edge.from,
  target: edge.to,
  label: edge.default ? '默认' : edge.condition_value === true ? '是' : edge.condition_value === false ? '否' : undefined,
  selected: state.selected?.kind === 'edge' && state.selected.id === edge.id,
}));
