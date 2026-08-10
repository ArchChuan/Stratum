import type { XYPosition } from '@xyflow/react';

import {
  WORKFLOW_LAYOUT_LAYER_GAP_X,
  WORKFLOW_LAYOUT_MARGIN,
  WORKFLOW_LAYOUT_NODE_GAP_Y,
  WORKFLOW_NODE_HEIGHT,
  WORKFLOW_NODE_WIDTH,
} from '@/constants';

import type { WorkflowSpec } from './workflow';

/** position 缺失或被填充 (0,0)（布局前坐标即原点）视为未定位。 */
export const hasValidPosition = (position: XYPosition | undefined): position is XYPosition =>
  position !== undefined && (position.x !== 0 || position.y !== 0);

/**
 * DAG 分层自动布局（参考 Dify 画布）：按边建邻接后 Kahn 拓扑分层，
 * 根节点 layer=1，子节点取所有父节点最大层 +1。环/断连节点无法定层时
 * 归入最大层兜底（纯函数不挂）。层内按 spec.nodes 原始出现顺序纵向
 * 居中排列，保证确定性（相同输入恒产出相同布局）。
 */
export const computeAutoLayout = (spec: WorkflowSpec): Record<string, XYPosition> => {
  const inDegree: Record<string, number> = {};
  const children: Record<string, string[]> = {};
  for (const node of spec.nodes) {
    inDegree[node.id] = 0;
    children[node.id] = [];
  }
  for (const edge of spec.edges) {
    if (!(edge.from in inDegree) || !(edge.to in inDegree)) continue;
    inDegree[edge.to] += 1;
    children[edge.from].push(edge.to);
  }

  const layer: Record<string, number> = {};
  const queue = spec.nodes.filter((node) => inDegree[node.id] === 0).map((node) => node.id);
  for (const id of queue) layer[id] = 1;
  for (let head = 0; head < queue.length; head += 1) {
    const id = queue[head];
    for (const child of children[id]) {
      inDegree[child] -= 1;
      layer[child] = Math.max(layer[child] || 1, layer[id] + 1);
      if (inDegree[child] === 0) queue.push(child);
    }
  }

  let maxLayer = 1;
  for (const value of Object.values(layer)) maxLayer = Math.max(maxLayer, value);
  const unlayered = spec.nodes.filter((node) => !layer[node.id]).map((node) => node.id);
  for (const id of unlayered) layer[id] = maxLayer;

  const column: Record<number, string[]> = {};
  for (const node of spec.nodes) {
    const depth = layer[node.id];
    (column[depth] ||= []).push(node.id);
  }

  const positions: Record<string, XYPosition> = {};
  const depths = Object.keys(column).map(Number).sort((a, b) => a - b);
  for (const depth of depths) {
    const ids = column[depth];
    const totalHeight = ids.length * WORKFLOW_NODE_HEIGHT + (ids.length - 1) * WORKFLOW_LAYOUT_NODE_GAP_Y;
    const x = WORKFLOW_LAYOUT_MARGIN + (depth - 1) * (WORKFLOW_NODE_WIDTH + WORKFLOW_LAYOUT_LAYER_GAP_X);
    ids.forEach((id, index) => {
      positions[id] = {
        x,
        y: WORKFLOW_LAYOUT_MARGIN + index * (WORKFLOW_NODE_HEIGHT + WORKFLOW_LAYOUT_NODE_GAP_Y) - totalHeight / 2,
      };
    });
  }
  return positions;
};

/** 为缺失坐标的节点填充自动布局值；已有合法 position 保留。immutable。 */
export const applyAutoLayout = (spec: WorkflowSpec): WorkflowSpec => {
  const missing = spec.nodes.some((node) => !hasValidPosition(node.position));
  if (!missing) return spec;
  const positions = computeAutoLayout(spec);
  return {
    ...spec,
    nodes: spec.nodes.map((node) => hasValidPosition(node.position) ? node : { ...node, position: positions[node.id] }),
  };
};
