import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ResourceSummary } from '../model/evaluation';

import { CreateEvaluationModal } from './CreateEvaluationModal';

const resources: ResourceSummary[] = [{
  id: 'r1', resource_id: 'skill-1', resource_kind: 'skill', status: 'active', stable_revision_id: 'v1',
  safe_summary: { name: '客服技能' }, created_at: '2026-07-23T00:00:00Z',
}];

describe('CreateEvaluationModal', () => {
  it('submits the authored case with contains assertion by default', async () => {
    const onClose = vi.fn();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<CreateEvaluationModal open resources={resources} onClose={onClose} onSubmit={onSubmit} />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '目标资源' }));
    fireEvent.click(await screen.findByText('客服技能（skill-1）'));
    fireEvent.change(screen.getByLabelText('评测名称'), { target: { value: '客服基线' } });
    fireEvent.change(screen.getByLabelText('用例名称'), { target: { value: '标准问候' } });
    fireEvent.change(screen.getByLabelText('测试输入'), { target: { value: '你好' } });
    fireEvent.change(screen.getByLabelText('期望输出'), { target: { value: '您好' } });
    fireEvent.click(screen.getByRole('button', { name: '创建并运行' }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      resource_id: 'r1', name: '客服基线', case_name: '标准问候',
      input: '你好', expected_output: '您好', assertion_mode: 'contains',
    }), resources[0]));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('collects tool_spec and step_judge from the always-visible process fields', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<CreateEvaluationModal open resources={resources} onClose={vi.fn()} onSubmit={onSubmit} />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '目标资源' }));
    fireEvent.click(await screen.findByText('客服技能（skill-1）'));
    fireEvent.change(screen.getByLabelText('评测名称'), { target: { value: '工具链路' } });
    fireEvent.change(screen.getByLabelText('用例名称'), { target: { value: '查天气' } });
    fireEvent.change(screen.getByLabelText('测试输入'), { target: { value: '北京天气' } });
    fireEvent.change(screen.getByLabelText('期望输出'), { target: { value: '晴天' } });

    const mustCall = screen.getByRole('combobox', { name: '必调用工具' });
    fireEvent.mouseDown(mustCall);
    fireEvent.change(mustCall, { target: { value: 'weather' } });
    fireEvent.keyDown(mustCall, { key: 'Enter', code: 'Enter', keyCode: 13 });
    fireEvent.change(screen.getByLabelText('步骤判定标准'), { target: { value: '逐步评分' } });

    fireEvent.click(screen.getByRole('button', { name: '创建并运行' }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      resource_id: 'r1', must_call: ['weather'], step_criteria: '逐步评分',
    }), resources[0]));
  });
});
