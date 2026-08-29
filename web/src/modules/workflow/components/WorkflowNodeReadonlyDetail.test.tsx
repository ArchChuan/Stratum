import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { WorkflowNode } from '../model/workflow';

import { WorkflowNodeReadonlyDetail } from './WorkflowNodeReadonlyDetail';

// Extract 限定 skill 变体，避免 Partial<WorkflowNode> 把联合类型里的 type 宽化
// 为 "skill" | "mcp_tool"，破坏按 type 字面量的判别收窄。
type SkillNode = Extract<WorkflowNode, { type: 'skill' }>;

const skillNode = (overrides: Partial<SkillNode> = {}): WorkflowNode => ({
  id: 'skill-node', name: '分析', type: 'skill', agent_id: 'agent-1', skill_id: 'skill-1', skill_revision_id: 'revision-3',
  input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0,
  ...overrides,
});

describe('WorkflowNodeReadonlyDetail', () => {
  it('prompts to select a node when none is selected', () => {
    render(<WorkflowNodeReadonlyDetail />);
    expect(screen.getByText('点击节点查看配置详情')).toBeInTheDocument();
  });

  it('renders the readable skill version label from the revision map', () => {
    render(<WorkflowNodeReadonlyDetail
      node={skillNode()}
      skillRevisionLabels={{ 'revision-3': '研究（已发布）' }}
    />);
    expect(screen.getByText('研究（已发布）')).toBeInTheDocument();
    // 可读 label 命中后，不再直接展示原始 revision ID。
    expect(screen.queryByText('revision-3')).not.toBeInTheDocument();
  });

  it('falls back to the raw revision id when the map has no entry', () => {
    render(<WorkflowNodeReadonlyDetail node={skillNode()} skillRevisionLabels={{ 'revision-2': '其他（已发布）' }} />);
    expect(screen.getByText('revision-3')).toBeInTheDocument();
  });

  it('omits the skill version row when no revision is fixed', () => {
    render(<WorkflowNodeReadonlyDetail node={skillNode({ skill_revision_id: '' })} />);
    expect(screen.queryByText('Skill 版本')).not.toBeInTheDocument();
  });
});
