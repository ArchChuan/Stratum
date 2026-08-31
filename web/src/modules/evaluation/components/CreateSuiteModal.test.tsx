import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { CreateSuiteModal } from './CreateSuiteModal';

describe('CreateSuiteModal', () => {
  it('submits the authored case with contains assertion by default', async () => {
    const onClose = vi.fn();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<CreateSuiteModal open onClose={onClose} onSubmit={onSubmit} />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '资源类型' }));
    fireEvent.click(await screen.findByText('技能'));
    fireEvent.change(screen.getByLabelText('套件名称'), { target: { value: '投诉分类基线' } });
    fireEvent.change(screen.getByLabelText('用例名称'), { target: { value: '物流' } });
    fireEvent.change(screen.getByLabelText('测试输入'), { target: { value: '快递没更新' } });
    fireEvent.change(screen.getByLabelText('期望输出'), { target: { value: '物流' } });
    fireEvent.click(screen.getByRole('button', { name: /创\s*建/ }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      resource_kind: 'skill', name: '投诉分类基线', case_name: '物流',
      input: '快递没更新', expected_output: '物流', assertion_mode: 'contains', enabled: true,
    })));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('collects the judge model and rubric when assertion is AI 判定', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<CreateSuiteModal open onClose={vi.fn()} onSubmit={onSubmit} />);

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '资源类型' }));
    fireEvent.click(await screen.findByText('技能'));
    fireEvent.change(screen.getByLabelText('套件名称'), { target: { value: '投诉分类基线' } });
    fireEvent.change(screen.getByLabelText('用例名称'), { target: { value: '总结判定' } });
    fireEvent.change(screen.getByLabelText('测试输入'), { target: { value: '帮我总结' } });
    fireEvent.change(screen.getByLabelText('期望输出'), { target: { value: '要点' } });
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '断言方式' }));
    fireEvent.click(await screen.findByText('AI 判定'));
    fireEvent.change(screen.getByLabelText('判定模型'), { target: { value: 'judge-v1' } });
    fireEvent.change(screen.getByLabelText('评分标准'), { target: { value: '总结要点覆盖度' } });
    fireEvent.click(screen.getByRole('button', { name: /创\s*建/ }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      resource_kind: 'skill', name: '投诉分类基线', case_name: '总结判定',
      input: '帮我总结', expected_output: '要点',
      assertion_mode: 'judge', judge_model: 'judge-v1', judge_rubric: '总结要点覆盖度', enabled: true,
    })));
  });
});
