import { describe, expect, it } from 'vitest';

import { hasCycle, referencedUpstream, upstreamNodes } from './graph';
import type { WorkflowEdge, WorkflowNode, WorkflowSpec } from './workflow';

const node = (id: string): WorkflowNode => ({
  id,
  name: id,
  type: 'agent',
  agent_id: '',
  input_mapping: {},
  output_mapping: {},
  retry: { max_attempts: 0, backoff_ms: 0 },
  timeout_ms: 0,
});

const edge = (from: string, to: string): WorkflowEdge => ({ id: `${from}-${to}`, from, to, default: false });

const spec = (nodes: WorkflowNode[], edges: WorkflowEdge[]): WorkflowSpec => ({
  nodes,
  edges,
  max_concurrency: 0,
});

describe('hasCycle', () => {
  it('returns false for a DAG', () => {
    expect(hasCycle(spec([node('a'), node('b'), node('c')], [edge('a', 'b'), edge('b', 'c')]))).toBe(false);
  });

  it('returns true for a self cycle', () => {
    expect(hasCycle(spec([node('a')], [edge('a', 'a')]))).toBe(true);
  });

  it('returns true for a 3-node cycle', () => {
    expect(hasCycle(spec([node('a'), node('b'), node('c')], [edge('a', 'b'), edge('b', 'c'), edge('c', 'a')]))).toBe(true);
  });

  it('returns false for a disconnected graph without cycles', () => {
    expect(hasCycle(spec([node('a'), node('b'), node('c')], [edge('a', 'b')]))).toBe(false);
  });

  it('ignores edges whose endpoints do not exist (half-finished draft)', () => {
    expect(hasCycle(spec([node('a')], [edge('a', 'ghost'), edge('ghost', 'a')]))).toBe(false);
  });
});

describe('upstreamNodes', () => {
  it('returns all reachable ancestors in spec order, without the node itself', () => {
    const s = spec([node('a'), node('b'), node('c'), node('d')], [edge('a', 'b'), edge('b', 'c'), edge('d', 'c')]);
    expect(upstreamNodes(s, 'c').map((n) => n.id)).toEqual(['a', 'b', 'd']);
  });

  it('returns an empty list for a root node', () => {
    const s = spec([node('a'), node('b')], [edge('a', 'b')]);
    expect(upstreamNodes(s, 'a').map((n) => n.id)).toEqual([]);
  });

  it('returns an empty list for a missing node id', () => {
    expect(upstreamNodes(spec([node('a')], []), 'ghost')).toEqual([]);
  });
});

describe('referencedUpstream', () => {
  it('parses nodes.<id>.output.<key> into the upstream id', () => {
    expect(referencedUpstream('nodes.A.output.summary')).toBe('A');
  });

  it('parses a whole-output reference', () => {
    expect(referencedUpstream('nodes.A.output')).toBe('A');
  });

  it('returns null for an empty node id', () => {
    expect(referencedUpstream('nodes..output.summary')).toBeNull();
  });

  it('returns null for non-reference values', () => {
    expect(referencedUpstream('${summary}')).toBeNull();
    expect(referencedUpstream('plain text')).toBeNull();
  });
});
