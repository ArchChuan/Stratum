import type { WorkflowNode, WorkflowSpec } from './workflow';

/**
 * 有向图工具集：环检测、上游节点解析。纯函数，供画布连线守卫与参数传递
 * 上游选择器复用。Kahn 逻辑与 layout.ts 的 computeAutoLayout 对齐：忽略
 * 端点不存在的边（半成品草稿），保证空图/断连图不误报。
 */
export const hasCycle = (spec: WorkflowSpec): boolean => {
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
  const queue = spec.nodes.filter((node) => inDegree[node.id] === 0).map((node) => node.id);
  let visited = 0;
  for (let head = 0; head < queue.length; head += 1) {
    visited += 1;
    for (const child of children[queue[head]]) {
      inDegree[child] -= 1;
      if (inDegree[child] === 0) queue.push(child);
    }
  }
  return visited < spec.nodes.length;
};

/** 返回 nodeId 的全部可达祖先（反向邻接 BFS，不含自身），按 spec.nodes 出现顺序。 */
export const upstreamNodes = (spec: WorkflowSpec, nodeId: string): WorkflowNode[] => {
  const parents: Record<string, string[]> = {};
  for (const node of spec.nodes) parents[node.id] = [];
  for (const edge of spec.edges) {
    if (!(edge.from in parents) || !(edge.to in parents)) continue;
    parents[edge.to].push(edge.from);
  }
  const seen = new Set<string>();
  const queue = [...(parents[nodeId] || [])];
  while (queue.length > 0) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    for (const parent of parents[id] || []) queue.push(parent);
  }
  return spec.nodes.filter((node) => seen.has(node.id));
};

/** 解析 nodes.<id>.output[.<key>] 形式的引用，返回上游节点 id；非引用返回 null。 */
export const referencedUpstream = (ref: string): string | null => {
  const parts = ref.split('.');
  if (parts[0] === 'nodes' && parts[2] === 'output' && parts[1]) return parts[1];
  return null;
};
