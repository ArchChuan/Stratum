import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { WorkflowNodeInspector } from './WorkflowNodeInspector';

const baseAgentNode = {
  id: 'node-1', name: '资料整理', type: 'agent' as const, agent_id: 'agent-1',
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
};

const openAdvanced = () => fireEvent.click(screen.getByText('高级设置'));

describe('WorkflowNodeInspector mapping fields', () => {
  it('writes a valid input_mapping JSON back as a structured mapping without dirty fields', () => {
    const onChange = vi.fn();
    render(<WorkflowNodeInspector
      node={baseAgentNode}
      onChange={onChange}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
    />);
    openAdvanced();
    const textarea = screen.getByLabelText('输入映射');
    fireEvent.change(textarea, { target: { value: '{"query":"$.task"}' } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      input_mapping: { query: '$.task' },
    }));
    const emitted = onChange.mock.calls[0][0];
    expect(emitted).not.toHaveProperty('input_mapping_text');
    expect(emitted.input_mapping).toEqual({ query: '$.task' });
  });

  it('applies the same contract to output_mapping', () => {
    const onChange = vi.fn();
    render(<WorkflowNodeInspector
      node={baseAgentNode}
      onChange={onChange}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
    />);
    openAdvanced();
    fireEvent.change(screen.getByLabelText('输出映射'), { target: { value: '{"summary":"$.result"}' } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      output_mapping: { summary: '$.result' },
    }));
    expect(onChange.mock.calls[0][0]).not.toHaveProperty('output_mapping_text');
  });

  it('keeps the last valid mapping and reports validation when the JSON is invalid', async () => {
    const onChange = vi.fn();
    render(<WorkflowNodeInspector
      node={{ ...baseAgentNode, input_mapping: { query: '$.task' } }}
      onChange={onChange}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
    />);
    openAdvanced();
    const textarea = screen.getByLabelText('输入映射');
    fireEvent.change(textarea, { target: { value: '{broken' } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      input_mapping: { query: '$.task' },
    }));
    expect(onChange.mock.calls[0][0]).not.toHaveProperty('input_mapping_text');
    expect(await screen.findByText('映射必须是合法的 JSON 对象，值必须是字符串')).toBeInTheDocument();
  });

  it('rejects non-string mapping values like {"a":5} that would break reload', async () => {
    const onChange = vi.fn();
    render(<WorkflowNodeInspector
      node={{ ...baseAgentNode, input_mapping: { query: '$.task' } }}
      onChange={onChange}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
    />);
    openAdvanced();
    const textarea = screen.getByLabelText('输入映射');
    fireEvent.change(textarea, { target: { value: '{"a":5}' } });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      input_mapping: { query: '$.task' },
    }));
    expect(await screen.findByText('映射必须是合法的 JSON 对象，值必须是字符串')).toBeInTheDocument();
  });
});
