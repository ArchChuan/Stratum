import { describe, expect, it } from 'vitest';

import {
  WORKFLOW_LAYOUT_LAYER_GAP_X,
  WORKFLOW_LAYOUT_MARGIN,
  WORKFLOW_LAYOUT_NODE_GAP_Y,
  WORKFLOW_NODE_HEIGHT,
  WORKFLOW_NODE_WIDTH,
} from '@/constants';

import { applyAutoLayout, computeAutoLayout, hasValidPosition } from './layout';
import type { WorkflowNode, WorkflowSpec } from './workflow';

const agent = (id: string, extra: Partial<WorkflowNode> = {}): WorkflowNode => ({
  id, name: id, type: 'agent', agent_id: 'a',
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
  ...extra,
});

describe('computeAutoLayout', () => {
  it('layers a diamond DAG by longest path', () => {
    // a → b → d；a → c → d：b/c 同层 2，d 层 3
    const spec: WorkflowSpec = {
      nodes: [agent('a'), agent('b'), agent('c'), agent('d')],
      edges: [
        { id: 'e1', from: 'a', to: 'b' }, { id: 'e2', from: 'a', to: 'c' },
        { id: 'e3', from: 'b', to: 'd' }, { id: 'e4', from: 'c', to: 'd' },
      ],
      max_concurrency: 0,
    };
    const layout = computeAutoLayout(spec);
    const xOf = (id: string) => layout[id].x;
    expect(xOf('a')).toBe(WORKFLOW_LAYOUT_MARGIN);
    expect(xOf('b')).toBe(xOf('c'));
    expect(xOf('d')).toBe(xOf('a') + (WORKFLOW_NODE_WIDTH + WORKFLOW_LAYOUT_LAYER_GAP_X) * 2);
  });

  it('lays out a condition with three branches on the same next layer', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('cond', { type: 'condition', condition: '1 == 1' }), agent('yes'), agent('no'), agent('def')],
      edges: [
        { id: 'e1', from: 'cond', to: 'yes', condition_value: true },
        { id: 'e2', from: 'cond', to: 'no', condition_value: false },
        { id: 'e3', from: 'cond', to: 'def', default: true },
      ],
      max_concurrency: 0,
    };
    const layout = computeAutoLayout(spec);
    expect(layout['yes'].x).toBe(layout['no'].x);
    expect(layout['no'].x).toBe(layout['def'].x);
    // 层内按 spec 原始顺序纵向排布，间隙一致
    expect(layout['yes'].y).toBeLessThan(layout['no'].y);
    expect(layout['no'].y).toBeLessThan(layout['def'].y);
    expect(layout['no'].y - layout['yes'].y).toBe(WORKFLOW_NODE_HEIGHT + WORKFLOW_LAYOUT_NODE_GAP_Y);
  });

  it('centers nodes vertically within their layer', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('a'), agent('b'), agent('c')],
      edges: [{ id: 'e1', from: 'a', to: 'b' }, { id: 'e2', from: 'a', to: 'c' }],
      max_concurrency: 0,
    };
    const layout = computeAutoLayout(spec);
    // 第二层 2 节点：总高 2H+GAP，起点 = MARGIN - 总高/2，两节点关于 MARGIN 对称
    const total = 2 * WORKFLOW_NODE_HEIGHT + WORKFLOW_LAYOUT_NODE_GAP_Y;
    expect(layout['b'].y).toBe(WORKFLOW_LAYOUT_MARGIN - total / 2);
    expect(layout['c'].y).toBe(layout['b'].y + WORKFLOW_NODE_HEIGHT + WORKFLOW_LAYOUT_NODE_GAP_Y);
  });

  it('keeps deterministic output for a fixed node order', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('a'), agent('b'), agent('c'), agent('d'), agent('e')],
      edges: [
        { id: 'e1', from: 'a', to: 'c' }, { id: 'e2', from: 'b', to: 'c' },
        { id: 'e3', from: 'c', to: 'd' }, { id: 'e4', from: 'd', to: 'e' },
      ],
      max_concurrency: 0,
    };
    expect(computeAutoLayout(spec)).toEqual(computeAutoLayout(JSON.parse(JSON.stringify(spec))));
  });

  it('falls back for cycles and disconnected nodes instead of hanging', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('a'), agent('b'), agent('orphan')],
      edges: [{ id: 'e1', from: 'a', to: 'b' }, { id: 'e2', from: 'b', to: 'a' }],
      max_concurrency: 0,
    };
    const layout = computeAutoLayout(spec);
    expect(layout['a'].x).toBe(WORKFLOW_LAYOUT_MARGIN);
    expect(layout['b'].x).toBe(WORKFLOW_LAYOUT_MARGIN);
    // 环内节点定层但孤儿节点归入最大层，全部有坐标
    expect(layout['orphan'].x).toBeGreaterThan(0);
    expect(Object.keys(layout)).toHaveLength(3);
  });
});

describe('applyAutoLayout', () => {
  it('keeps existing positions and fills only the missing ones', () => {
    const positioned = { x: 500, y: -120 };
    const spec: WorkflowSpec = {
      nodes: [agent('a', { position: positioned }), agent('b')],
      edges: [{ id: 'e1', from: 'a', to: 'b' }],
      max_concurrency: 0,
    };
    const next = applyAutoLayout(spec);
    expect(next.nodes[0].position).toEqual(positioned);
    expect(next.nodes[1].position).toBeDefined();
    expect(spec.nodes[1].position).toBeUndefined();
  });

  it('treats (0,0) as unpositioned and fills it', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('a', { position: { x: 0, y: 0 } }), agent('b', { position: { x: 240, y: 60 } })],
      edges: [{ id: 'e1', from: 'a', to: 'b' }],
      max_concurrency: 0,
    };
    const next = applyAutoLayout(spec);
    // 单节点层内居中：起点 = MARGIN - H/2
    expect(next.nodes[0].position).toEqual({ x: WORKFLOW_LAYOUT_MARGIN, y: WORKFLOW_LAYOUT_MARGIN - WORKFLOW_NODE_HEIGHT / 2 });
    expect(next.nodes[1].position).toEqual({ x: 240, y: 60 });
  });

  it('is idempotent and does not mutate the input spec', () => {
    const spec: WorkflowSpec = {
      nodes: [agent('a'), agent('b')],
      edges: [{ id: 'e1', from: 'a', to: 'b' }],
      max_concurrency: 0,
    };
    const once = applyAutoLayout(spec);
    expect(applyAutoLayout(once)).toEqual(once);
    expect(spec.nodes[0].position).toBeUndefined();
  });

  it('hasValidPosition accepts only explicit non-zero coordinates', () => {
    expect(hasValidPosition(undefined)).toBe(false);
    expect(hasValidPosition({ x: 0, y: 0 })).toBe(false);
    expect(hasValidPosition({ x: 1, y: 0 })).toBe(true);
    expect(hasValidPosition({ x: 0, y: -1 })).toBe(true);
  });
});
