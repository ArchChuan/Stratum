import { render, screen } from '@testing-library/react';
import { type NodeProps } from '@xyflow/react';
import { describe, expect, it, vi } from 'vitest';

import type { WorkflowFlowNode, WorkflowNodeData } from '../../model/editor';
import type { WorkflowNode } from '../../model/workflow';

import { WorkflowNodeCard } from './WorkflowNodeCard';

vi.mock('@xyflow/react', () => ({
  Handle: ({ type, id }: { type: string; id?: string }) =>
    <span data-testid={`handle-${type}`} data-handleid={id ?? null} />,
  Position: { Left: 'left', Right: 'right' },
}));

const baseNode = {
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
};

const cardProps = (node: WorkflowNode, overrides: Partial<WorkflowNodeData> = {}): NodeProps<WorkflowFlowNode> =>
  ({ id: 'n1', type: 'workflowNode', data: { node, selected: false, ...overrides } }) as NodeProps<WorkflowFlowNode>;

describe('WorkflowNodeCard', () => {
  it('renders three labelled branch handles for a condition node', () => {
    render(<WorkflowNodeCard {...cardProps({
      id: 'n1', type: 'condition', name: '分支', agent_id: '', condition: '1 == 1', ...baseNode,
    })} />);
    expect(screen.getByRole('article', { name: '分支节点' })).toBeInTheDocument();
    const sources = screen.getAllByTestId('handle-source');
    expect(sources).toHaveLength(3);
    expect(sources.map((handle) => handle.getAttribute('data-handleid'))).toEqual(['yes', 'no', 'default']);
    expect(screen.getByText('是')).toBeInTheDocument();
    expect(screen.getByText('否')).toBeInTheDocument();
    expect(screen.getByText('默认')).toBeInTheDocument();
  });

  it('renders a single id-less source handle for non-condition nodes', () => {
    render(<WorkflowNodeCard {...cardProps({
      id: 'n2', type: 'approval', name: '审批', agent_id: '', ...baseNode,
    })} />);
    const sources = screen.getAllByTestId('handle-source');
    expect(sources).toHaveLength(1);
    // 非 condition 边不派生 sourceHandle（editor toFlowEdges），handle 必须无 id，否则连线渲染失败
    expect(sources[0].getAttribute('data-handleid')).toBeNull();
  });

  it('applies the selected class when selected', () => {
    render(<WorkflowNodeCard {...cardProps({
      id: 'n3', type: 'agent', name: 'Agent', agent_id: 'a1', ...baseNode,
    }, { selected: true })} />);
    expect(screen.getByRole('article', { name: 'Agent节点' })).toHaveClass('is-selected');
  });
});
