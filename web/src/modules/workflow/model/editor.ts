import { MarkerType, type Edge, type Node, type XYPosition } from '@xyflow/react';

import { hasCycle } from './graph';
import type { WorkflowEdge, WorkflowNode, WorkflowNodeType, WorkflowSpec } from './workflow';

export type EditorSelection = { kind: 'node' | 'edge'; id: string } | null;

export interface WorkflowEditorState {
  spec: WorkflowSpec;
  selected: EditorSelection;
  dirty: boolean;
}

export type WorkflowEditorAction =
  | { type: 'server.reset'; spec: WorkflowSpec }
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
  /** 可编辑画布注入删除回调（只读/运行画布不传 → 不渲染删除按钮）。 */
  onDelete?: (nodeId: string) => void;
}

export type WorkflowFlowNode = Node<WorkflowNodeData, 'workflowNode'>;

/**
 * 解析 inspector 的映射文本。契约约束（zod z.record(z.string()) 与 Go
 * map[string]string）：必须是纯对象且每个 value 为 string。`{"a":5}` 一旦
 * 保存，工作流重载会直接失败，因此这里双重断言。
 * 返回 null 表示非法输入；空字符串视为未修改，沿用 previous。
 */
export const parseMappingText = (
  text: string,
  previous: Record<string, string>,
): Record<string, string> | null => {
  if (typeof text !== 'string' || text.trim() === '') return previous;
  try {
    const parsed: unknown = JSON.parse(text);
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') return null;
    for (const value of Object.values(parsed)) {
      if (typeof value !== 'string') return null;
    }
    return parsed as Record<string, string>;
  } catch {
    return null;
  }
};

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
  selected: null,
  dirty: false,
});

export const workflowEditorReducer = (
  state: WorkflowEditorState,
  action: WorkflowEditorAction,
): WorkflowEditorState => {
  switch (action.type) {
    case 'server.reset':
      return { spec: action.spec, selected: null, dirty: false };
    case 'node.insert':
      if (state.spec.nodes.some((node) => node.id === action.nodeId)) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          nodes: [...state.spec.nodes, { ...createNode(action.nodeId, action.nodeType), position: action.position }],
        },
        selected: { kind: 'node', id: action.nodeId },
        dirty: true,
      };
    case 'node.move':
      return {
        ...state,
        spec: {
          ...state.spec,
          nodes: state.spec.nodes.map((node) => node.id === action.nodeId ? { ...node, position: action.position } : node),
        },
        dirty: true,
      };
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
      // 纯守卫：构造候选 spec 后 Kahn 检测，成环则整体拒绝（reducer 不弹 toast，
      // UI 提示由画布 connect 回调负责，保持 reducer 无副作用）。
      const candidate: WorkflowSpec = { ...state.spec, edges: [...state.spec.edges, edge] };
      if (hasCycle(candidate)) return state;
      return {
        ...state,
        spec: candidate,
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

export const toFlowNodes = (state: WorkflowEditorState, onDelete?: (nodeId: string) => void): WorkflowFlowNode[] => state.spec.nodes.map((node) => ({
  id: node.id,
  type: 'workflowNode',
  position: node.position || { x: 0, y: 0 },
  data: {
    node,
    selected: state.selected?.kind === 'node' && state.selected.id === node.id,
    onDelete,
  },
}));

const conditionSourceHandle = (edge: WorkflowEdge): string | undefined => {
  if (edge.default) return 'default';
  if (edge.condition_value === true) return 'yes';
  if (edge.condition_value === false) return 'no';
  // 裸边（无分支字段）运行时恒不选中是死边：挂 default handle 会与真 default 边
  // 视觉混淆且诱使用户重连出双 default（保存必报错），独立标记为「未指定分支」。
  return 'default';
};

export const toFlowEdges = (state: WorkflowEditorState): Edge[] => state.spec.edges.map((edge) => {
  // 仅 condition 源边派生 sourceHandle：非 condition 节点 handle 无 id，
  // 若派生 handle id React Flow 找不到对应 handle 会让整条边不渲染。
  const sourceNode = state.spec.nodes.find((node) => node.id === edge.from);
  const isConditionEdge = sourceNode?.type === 'condition';
  const isBareConditionEdge = isConditionEdge && !edge.default && edge.condition_value === undefined;
  return {
    id: edge.id || `${edge.from}-${edge.to}`,
    source: edge.from,
    target: edge.to,
    sourceHandle: isConditionEdge ? conditionSourceHandle(edge) : undefined,
    // 方向箭头：从源指向目标，直观展示先后依赖关系。三处画布（设计/只读/运行）
    // 共用本派生源，一处生效。只读/运行画布 spread 保留 markerEnd。
    markerEnd: { type: MarkerType.ArrowClosed },
    label: isBareConditionEdge
      ? '未指定分支'
      : edge.default ? '默认' : edge.condition_value === true ? '是' : edge.condition_value === false ? '否' : undefined,
    selected: state.selected?.kind === 'edge' && state.selected.id === edge.id,
  };
});
