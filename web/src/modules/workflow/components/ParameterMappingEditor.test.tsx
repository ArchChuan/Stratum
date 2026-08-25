import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { WorkflowNode } from '../model/workflow';

import { ParameterMappingEditor } from './ParameterMappingEditor';

const upstream = (id: string): WorkflowNode => ({
  id,
  name: id,
  type: 'agent',
  agent_id: '',
  input_mapping: {},
  output_mapping: {},
  retry: { max_attempts: 0, backoff_ms: 0 },
  timeout_ms: 0,
});

const openSelect = (label: string) => {
  // AntD Select 在外层 .ant-select 与内层 search input 上都挂了 aria-label，
  // getByLabelText 会命中两个元素，取 DOM 顺序第一个（外层容器）用于展开。
  const element = screen.getAllByLabelText(label)[0];
  const selector = element.closest('.ant-select')?.querySelector('.ant-select-selector');
  fireEvent.mouseDown(selector ?? element);
};

describe('ParameterMappingEditor', () => {
  it('renders existing mapping rows and drops blank keys on save', () => {
    const onChange = vi.fn();
    render(<ParameterMappingEditor
      direction="input"
      mapping={{ topic: 'hello', empty: 'x' }}
      upstreams={[]}
      onChange={onChange}
    />);
    const nameInputs = screen.getAllByLabelText('输入映射参数名');
    const valueInputs = screen.getAllByLabelText('输入映射参数值');
    expect(nameInputs).toHaveLength(2);
    expect(nameInputs[0]).toHaveValue('topic');
    expect(valueInputs[0]).toHaveValue('hello');
    // 清空 key → 该行不再落库，只保留非空 key 的映射。
    fireEvent.change(nameInputs[0], { target: { value: '' } });
    expect(onChange).toHaveBeenLastCalledWith({ empty: 'x' });
  });

  it('inserts an upstream output reference into the value', async () => {
    const onChange = vi.fn();
    render(<ParameterMappingEditor
      direction="input"
      mapping={{}}
      upstreams={[upstream('node-1')]}
      onChange={onChange}
    />);
    fireEvent.click(screen.getByRole('button', { name: /添加参数/ }));
    fireEvent.change(screen.getByLabelText('输入映射参数名'), { target: { value: 'result' } });
    openSelect('输入映射引用上游节点');
    await waitFor(() => {
      expect(document.querySelectorAll('.ant-select-item-option').length).toBeGreaterThan(0);
    });
    fireEvent.click(document.querySelectorAll('.ant-select-item-option')[0]);
    fireEvent.change(screen.getByLabelText('输入映射引用输出字段'), { target: { value: 'summary' } });
    fireEvent.click(screen.getByRole('button', { name: '插入引用' }));
    // 引用格式与后端 nodeInput 的 nodes.<id>.output.<key> 对齐。
    expect(onChange).toHaveBeenLastCalledWith({ result: 'nodes.node-1.output.summary' });
  });

  it('replaces a non-empty value with the reference instead of mixing text', async () => {
    const onChange = vi.fn();
    render(<ParameterMappingEditor
      direction="input"
      mapping={{ result: '已手写的值' }}
      upstreams={[upstream('node-1')]}
      onChange={onChange}
    />);
    openSelect('输入映射引用上游节点');
    await waitFor(() => {
      expect(document.querySelectorAll('.ant-select-item-option').length).toBeGreaterThan(0);
    });
    fireEvent.click(document.querySelectorAll('.ant-select-item-option')[0]);
    fireEvent.change(screen.getByLabelText('输入映射引用输出字段'), { target: { value: 'summary' } });
    fireEvent.click(screen.getByRole('button', { name: '插入引用' }));
    // 后端 resolveMappingReference 只识别以 nodes. 开头的纯引用，混排文本会被
    // 整条静默丢弃，故值非空时整体替换，不允许 "hello nodes.A.output.x" 混排。
    expect(onChange).toHaveBeenLastCalledWith({ result: 'nodes.node-1.output.summary' });
  });

  it('only lists agent/skill nodes as referenceable upstreams', async () => {
    const onChange = vi.fn();
    const conditionNode: WorkflowNode = {
      id: 'cond-1',
      name: 'cond-1',
      type: 'condition',
      condition: '$.ok == true',
      agent_id: '',
      input_mapping: {},
      output_mapping: {},
      retry: { max_attempts: 0, backoff_ms: 0 },
      timeout_ms: 0,
    };
    render(<ParameterMappingEditor
      direction="input"
      mapping={{}}
      upstreams={[upstream('agent-1'), conditionNode]}
      onChange={onChange}
    />);
    fireEvent.click(screen.getByRole('button', { name: /添加参数/ }));
    openSelect('输入映射引用上游节点');
    await waitFor(() => {
      expect(document.querySelectorAll('.ant-select-item-option').length).toBeGreaterThan(0);
    });
    const labels = Array.from(document.querySelectorAll('.ant-select-item-option'))
      .map((el) => el.textContent);
    // condition/mcp_tool 输出不是契约 JSON，引用其字段必然 run 失败，故不列出。
    expect(labels).toContain('agent-1');
    expect(labels).not.toContain('cond-1');
  });

  it('omits the upstream-reference UI on output mapping', () => {
    const onChange = vi.fn();
    render(<ParameterMappingEditor
      direction="output"
      mapping={{}}
      upstreams={[upstream('node-1')]}
      onChange={onChange}
    />);
    fireEvent.click(screen.getByRole('button', { name: /添加参数/ }));
    // 输出映射契约是 $ / $.path JSONPath selector，插入 nodes.<id>.output.<field>
    // 发布时必报 invalid output selector，故 output 方向不渲染引用插入 UI。
    expect(screen.queryByRole('button', { name: '插入引用' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('输出映射引用上游节点')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('输出映射引用输出字段')).not.toBeInTheDocument();
  });
});
