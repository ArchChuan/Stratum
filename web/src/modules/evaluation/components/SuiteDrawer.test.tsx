import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Modal } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { SuiteSummary } from '../model/evaluation';

import { SuiteDrawer } from './SuiteDrawer';

const apiMocks = vi.hoisted(() => ({
  getSuiteDraft: vi.fn(),
  updateDraftCase: vi.fn(),
  generateSuiteCases: vi.fn(),
  publishSuite: vi.fn(),
}));
vi.mock('../api/evaluation.api', () => ({ evaluationApi: apiMocks }));

// 组件成功后调用 message.success/error，mock 掉避免 rc-notification 定时器在
// teardown 后触发 setState。
const messageMocks = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock('antd', async () => ({
  ...(await vi.importActual<typeof import('antd')>('antd')),
  message: { success: messageMocks.success, error: messageMocks.error },
}));

const suite: SuiteSummary = { id: 's1', name: '投诉分类基线', description: '技能检索基线', status: 'draft', created_at: '2026-07-23T00:00:00Z' };
const draft = {
  id: 'rev-1', suite_id: 's1', version_no: 1, status: 'draft', resource_kind: 'skill',
  cases: [
    { id: 'c1', name: '标准问候', input: '你好', expected_output: '您好', assertion_mode: 'contains', enabled: true },
    { id: 'c2', name: '总结判定', input: '帮我总结', expected_output: '要点', assertion_mode: 'judge',
      judge_spec: { model: 'judge-v1', rubric: '总结要点覆盖度' }, enabled: true,
      source_trace_id: 'trace-1', generate_reason: '负样本优先' },
    { id: 'c3', name: '工具链路', input: '查天气', expected_output: '晴天', assertion_mode: 'contains',
      tool_spec: { must_call: ['weather'], must_not_call: ['delete'], order: ['search', 'weather'], max_calls: 5 },
      step_judge: { criteria: '逐步评分' }, enabled: true },
  ],
};

describe('SuiteDrawer', () => {
  beforeEach(() => {
    apiMocks.getSuiteDraft.mockReset();
    apiMocks.publishSuite.mockReset();
    apiMocks.updateDraftCase.mockReset();
    apiMocks.generateSuiteCases.mockReset();
    messageMocks.success.mockClear();
    messageMocks.error.mockClear();
    apiMocks.getSuiteDraft.mockResolvedValue(draft);
    apiMocks.publishSuite.mockResolvedValue({ ...draft, status: 'published', version_no: 1 });
  });

  it('loads the draft and surfaces judge spec and provenance for audit', async () => {
    render(<SuiteDrawer suite={suite} open onClose={vi.fn()} canManage onChanged={vi.fn()} />);

    expect(await screen.findByText('标准问候')).toBeInTheDocument();
    expect(apiMocks.getSuiteDraft).toHaveBeenCalledWith('s1');
    expect(screen.getByText('总结判定')).toBeInTheDocument();
    expect(screen.getByText(/judge-v1/)).toBeInTheDocument();
    expect(screen.getByText(/trace-1/)).toBeInTheDocument();
    expect(screen.getByText('负样本优先')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /编\s*辑/ })).toHaveLength(3);
  });

  it('surfaces tool_spec and step_judge summaries for audit', async () => {
    render(<SuiteDrawer suite={suite} open onClose={vi.fn()} canManage onChanged={vi.fn()} />);

    expect(await screen.findByText('工具链路')).toBeInTheDocument();
    expect(screen.getByText(/必调用:weather/)).toBeInTheDocument();
    expect(screen.getByText(/禁调用:delete/)).toBeInTheDocument();
    expect(screen.getByText(/上限:5/)).toBeInTheDocument();
    expect(screen.getByText(/步骤判定：逐步评分/)).toBeInTheDocument();
  });

  it('publishes after confirmation and closes with a refresh', async () => {
    let confirmOptions: { onOk: () => Promise<void> } | undefined;
    const confirmSpy = vi.spyOn(Modal, 'confirm').mockImplementation((options) => {
      confirmOptions = options as { onOk: () => Promise<void> };
      return { destroy: vi.fn() } as never;
    });
    const onChanged = vi.fn();
    const onClose = vi.fn();
    render(<SuiteDrawer suite={suite} open onClose={onClose} canManage onChanged={onChanged} />);
    await screen.findByText('标准问候');

    fireEvent.click(screen.getByRole('button', { name: /发\s*布/ }));
    expect(confirmSpy).toHaveBeenCalled();
    await confirmOptions!.onOk();

    await waitFor(() => expect(apiMocks.publishSuite).toHaveBeenCalledWith('s1'));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    confirmSpy.mockRestore();
  });
});
