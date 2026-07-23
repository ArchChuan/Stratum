import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Button, Form } from 'antd';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { WorkflowInputSchemaEditor } from './WorkflowInputSchemaEditor';
import { WorkflowMetadataForm } from './WorkflowMetadataForm';
import { WorkflowNodeInspector } from './WorkflowNodeInspector';

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })));
});

describe('workflow configuration', () => {
  it('requires workflow name and task label and rejects duplicate input keys', async () => {
    const onFinish = vi.fn();
    render(<Form onFinish={onFinish} initialValues={{ fields: [{ key: 'topic', label: '主题', type: 'short_text' }, { key: 'topic', label: '备用主题', type: 'long_text' }] }}>
      <WorkflowMetadataForm />
      <WorkflowInputSchemaEditor />
      <Button aria-label="保存" htmlType="submit">保存</Button>
    </Form>);
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(await screen.findByText('请输入工作流名称')).toBeInTheDocument();
    expect(await screen.findByText('请输入任务名称')).toBeInTheDocument();
    expect((await screen.findAllByText('字段标识不能重复')).length).toBeGreaterThan(0);
    expect(onFinish).not.toHaveBeenCalled();
  });

  it('offers all approved input field types', async () => {
    render(<Form initialValues={{ fields: [{ key: 'topic', label: '主题', type: 'short_text' }] }}><WorkflowInputSchemaEditor /></Form>);
    fireEvent.mouseDown(screen.getAllByLabelText('字段类型')[0]);
    for (const label of ['短文本', '长文本', '数字', '单选', '多选', '开关', '日期']) {
      expect((await screen.findAllByText(label)).length).toBeGreaterThan(0);
    }
  });

  it('shows type-specific node fields and keeps advanced settings collapsed', async () => {
    const onChange = vi.fn();
    render(<WorkflowNodeInspector
      node={{ id: 'skill-1', type: 'skill', name: '分析', agent_id: '', skill_id: '', skill_revision_id: '', input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0 }}
      onChange={onChange}
      agents={[{ value: 'agent-1', label: '研究 Agent' }]}
      skills={[{ value: 'skill-1', label: '研究 Skill' }]}
      skillRevisions={[{ value: 'revision-3', label: '修订 3' }]}
      mcpServers={[]}
    />);
    expect(screen.getByLabelText('固定 Skill 修订')).toBeInTheDocument();
    expect(screen.queryByLabelText('最大重试次数')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('高级设置'));
    expect(await screen.findByLabelText('最大重试次数')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('节点名称'), { target: { value: '深度分析' } });
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ name: '深度分析' })));
  });
});
