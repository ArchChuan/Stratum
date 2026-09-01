import { render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { StepJudgeFields, ToolSpecFields } from './CaseFields';

import { EVALUATION_MAX_CALLS_LIMIT } from '@/constants';

describe('ToolSpecFields', () => {
  it('renders the deterministic tool-call controls with the platform max_calls limit', () => {
    render(<Form><ToolSpecFields /></Form>);
    expect(screen.getByRole('combobox', { name: '必调用工具' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '禁止调用工具' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '调用顺序' })).toBeInTheDocument();
    expect(screen.getByLabelText('最大调用次数')).toHaveAttribute('aria-valuemax', String(EVALUATION_MAX_CALLS_LIMIT));
  });
});

describe('StepJudgeFields', () => {
  it('renders the step-level LLM rubric textarea', () => {
    render(<Form><StepJudgeFields /></Form>);
    expect(screen.getByLabelText('步骤判定标准')).toBeInTheDocument();
  });
});
