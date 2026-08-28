import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { WorkflowNode } from '../model/workflow';

import { WorkflowNodeInspector } from './WorkflowNodeInspector';

const baseAgentNode: WorkflowNode = {
  id: 'node-1', name: '资料整理', type: 'agent', agent_id: 'agent-1',
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
};

const skillNode = (overrides: Partial<WorkflowNode> = {}): WorkflowNode => ({
  id: 'skill-node', name: '分析', agent_id: 'agent-1', skill_id: 'skill-1', skill_revision_id: 'revision-3',
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
  ...overrides,
  type: 'skill',
});

const openSelect = (label: string) => {
  const element = screen.getByLabelText(label);
  const selector = element.closest('.ant-select')?.querySelector('.ant-select-selector');
  fireEvent.mouseDown(selector ?? element);
};

/** AntD Select 下拉项（渲染在 body portal），文本为 option 的 label。 */
const dropdownOptionTexts = () =>
  Array.from(document.querySelectorAll('.ant-select-item-option'))
    .map((el) => el.textContent ?? '');

describe('WorkflowNodeInspector', () => {
  it('renders type-specific fields and parameter mapping editors', () => {
    render(<WorkflowNodeInspector
      node={skillNode()}
      onChange={vi.fn()}
      onDelete={vi.fn()}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
      upstreams={[]} agentAllowedSkills={{}}
    />);
    expect(screen.getByLabelText('Skill 版本')).toBeInTheDocument();
    expect(screen.getByText('输入映射')).toBeInTheDocument();
    expect(screen.getByText('输出映射')).toBeInTheDocument();
  });

  it('filters skills by the selected agent allowedSkills', async () => {
    render(<WorkflowNodeInspector
      node={skillNode()}
      onChange={vi.fn()}
      onDelete={vi.fn()}
      agents={[]}
      skills={[{ value: 'skill-1', label: '研究 Skill' }, { value: 'skill-2', label: '写作 Skill' }]}
      skillRevisions={[]}
      mcpServers={[]}
      upstreams={[]}
      agentAllowedSkills={{ 'agent-1': ['skill-1'] }}
    />);
    openSelect('Skill');
    await waitFor(() => expect(dropdownOptionTexts()).toEqual(['研究 Skill']));
  });

  it('renders an empty skill list when the agent has empty allowedSkills', async () => {
    render(<WorkflowNodeInspector
      node={skillNode()}
      onChange={vi.fn()}
      onDelete={vi.fn()}
      agents={[]}
      skills={[{ value: 'skill-1', label: '研究 Skill' }]}
      skillRevisions={[]}
      mcpServers={[]}
      upstreams={[]}
      agentAllowedSkills={{ 'agent-1': [] }}
    />);
    openSelect('Skill');
    // 空 allowedSkills = 无技能 → 下拉无选项。
    await waitFor(() => expect(dropdownOptionTexts()).toEqual([]));
  });

  it('filters agents by the selected skill (reverse linkage)', async () => {
    render(<WorkflowNodeInspector
      node={skillNode()}
      onChange={vi.fn()}
      onDelete={vi.fn()}
      agents={[{ value: 'agent-1', label: '研究 Agent' }, { value: 'agent-2', label: '写作 Agent' }]}
      skills={[{ value: 'skill-1', label: '研究 Skill' }, { value: 'skill-2', label: '写作 Skill' }]}
      skillRevisions={[]}
      mcpServers={[]}
      upstreams={[]}
      agentAllowedSkills={{ 'agent-1': ['skill-1'], 'agent-2': ['skill-2'] }}
    />);
    // 已选 skill-1 → agent 列表只保留允许 skill-1 的 agent（反向联动）。
    openSelect('执行 Agent');
    await waitFor(() => expect(dropdownOptionTexts()).toEqual(['研究 Agent']));
  });

  it('deletes the node from the bottom button', () => {
    const onDelete = vi.fn();
    render(<WorkflowNodeInspector
      node={baseAgentNode}
      onChange={vi.fn()}
      onDelete={onDelete}
      agents={[]} skills={[]} skillRevisions={[]} mcpServers={[]}
      upstreams={[]} agentAllowedSkills={{}}
    />);
    fireEvent.click(screen.getByRole('button', { name: '删除节点' }));
    expect(onDelete).toHaveBeenCalledWith('node-1');
  });
});
