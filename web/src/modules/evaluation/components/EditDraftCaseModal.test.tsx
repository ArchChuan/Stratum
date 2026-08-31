import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { EvaluationCase } from '../model/evaluation';

import { EditDraftCaseModal } from './EditDraftCaseModal';

const containsCase: EvaluationCase = {
  id: 'c1', name: '标准问候', input: '你好', expected_output: '您好',
  assertion_mode: 'contains', enabled: true,
};

describe('EditDraftCaseModal', () => {
  it('pre-fills from the draft and submits camel-cased values on save', async () => {
    const onClose = vi.fn();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<EditDraftCaseModal open draft={containsCase} onClose={onClose} onSubmit={onSubmit} />);

    expect(screen.getByLabelText('用例名称')).toHaveValue('标准问候');
    expect(screen.getByLabelText('测试输入')).toHaveValue('你好');
    expect(screen.getByLabelText('期望输出')).toHaveValue('您好');
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({
      name: '标准问候', input: '你好', expectedOutput: '您好', assertionMode: 'contains', enabled: true,
    }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('explains that the judge spec is immutable when editing a judge case', () => {
    const judgeCase: EvaluationCase = { ...containsCase, name: '总结判定', assertion_mode: 'judge' };
    render(<EditDraftCaseModal open draft={judgeCase} onClose={vi.fn()} onSubmit={vi.fn()} />);
    expect(screen.getByText(/不可修改/)).toBeInTheDocument();
  });
});
