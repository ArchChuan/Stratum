import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { message } from 'antd';
import { vi } from 'vitest';

import type { MatrixReport } from '../model/mechanism';
import { MatrixWorkbench } from './MatrixWorkbench';

const { mechanismApiMock } = vi.hoisted(() => ({
  mechanismApiMock: {
    matrixReport: vi.fn(),
    runMatrix: vi.fn(),
    adopt: vi.fn(),
  },
}));

vi.mock('../api/mechanism.api', () => ({ mechanismApi: mechanismApiMock }));

// antd Table 依赖 ResizeObserver，jsdom 无原生实现。
class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Object.defineProperty(window, 'ResizeObserver', {
  writable: true,
  value: ResizeObserverMock,
});

const report: MatrixReport = {
  suites: [{ id: 'suite-1', name: '机制基准集', description: '', active_revision: 'v3', case_count: 3 }],
  cells: [
    {
      family_key: 'qwen',
      display_name: 'Qwen',
      status: 'active',
      version: 2,
      fingerprint: 'fp-q',
      enrich_model: 'qwen-max',
      summary_model: '',
      run_id: 'run-1',
      passed: true,
      pass_rate: 0.9,
      total_cost: 0.12,
      avg_latency: 300,
      total_cases: 3,
      frontier: true,
    },
    {
      family_key: 'deepseek',
      display_name: 'DeepSeek',
      status: 'draft',
      version: 1,
      fingerprint: 'fp-d',
      enrich_model: '',
      summary_model: '',
      run_id: 'run-2',
      passed: false,
      pass_rate: 0.4,
      total_cost: 0.05,
      avg_latency: 150,
      total_cases: 3,
      frontier: false,
    },
  ],
  frontier_keys: ['qwen'],
};

const renderWorkbench = () => render(<MatrixWorkbench />);

beforeEach(() => {
  vi.clearAllMocks();
  mechanismApiMock.matrixReport.mockResolvedValue(report);
});

it('渲染基准集信息与矩阵单元格，标注帕累托前沿', async () => {
  renderWorkbench();
  expect(await screen.findByText('机制基准集')).toBeInTheDocument();
  expect(screen.getByText(/已发布 v3/)).toBeInTheDocument();
  expect(screen.getByText(/3 个用例/)).toBeInTheDocument();

  expect(screen.getByText('qwen')).toBeInTheDocument();
  expect(screen.getByText('Qwen')).toBeInTheDocument();
  // 指标格式化：通过率百分比 / 成本四位小数 / 延迟毫秒
  expect(screen.getByText('90.0%')).toBeInTheDocument();
  expect(screen.getByText('$0.1200')).toBeInTheDocument();
  expect(screen.getByText('300ms')).toBeInTheDocument();
  // 前沿标注：active+frontier → 帕累托前沿，draft+非前沿 → 受支配
  expect(screen.getByText('帕累托前沿')).toBeInTheDocument();
  expect(screen.getByText('受支配')).toBeInTheDocument();
});

it('未评测档案不显示指标，显示占位符', async () => {
  mechanismApiMock.matrixReport.mockResolvedValue({
    suites: [],
    cells: [{
      family_key: 'ghost', display_name: 'Ghost', status: 'draft', version: 1,
      fingerprint: '', enrich_model: '', summary_model: '', passed: false,
    }],
    frontier_keys: [],
  });
  renderWorkbench();
  expect(await screen.findByText('Ghost')).toBeInTheDocument();
  expect(screen.getByText('未评测')).toBeInTheDocument();
  // 无 run 的单元格不参与前沿（占位 '-' 而非受支配/前沿）
  expect(screen.queryByText('受支配')).not.toBeInTheDocument();
});

it('无档案时显示空态提示', async () => {
  mechanismApiMock.matrixReport.mockResolvedValue({ suites: [], cells: [], frontier_keys: [] });
  renderWorkbench();
  expect(await screen.findByText(/暂无档案，请先到「模型档案」页建档/)).toBeInTheDocument();
  expect(screen.getByText(/尚无基准集/)).toBeInTheDocument();
});

it('触发评测需确认，确认后排队并刷新', async () => {
  mechanismApiMock.runMatrix.mockResolvedValue({ suite_revision_id: 'suite-rev-1', triggered_count: 2 });
  renderWorkbench();
  await screen.findByText('机制基准集');

  fireEvent.click(screen.getByRole('button', { name: /触发评测/ }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText(/将为全部档案/)).toBeInTheDocument();

  fireEvent.click(within(dialog).getByRole('button', { name: /触发评测/ }));
  await waitFor(() => expect(mechanismApiMock.runMatrix).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(mechanismApiMock.matrixReport).toHaveBeenCalledTimes(2));
});

it('采纳仅对 draft 档案可用，确认后采纳并刷新', async () => {
  mechanismApiMock.adopt.mockResolvedValue({ family_key: 'deepseek', status: 'draft' });
  renderWorkbench();
  await screen.findByText('qwen');

  // active 档案（qwen）无采纳按钮，仅 draft（deepseek）有
  // antd 双汉字按钮自动插空格（"采 纳"），用 \s* 兼容
  expect(screen.getAllByRole('button', { name: /采\s*纳/ })).toHaveLength(1);

  fireEvent.click(screen.getByRole('button', { name: /采\s*纳/ }));
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('button', { name: /采\s*纳/ }));

  await waitFor(() => expect(mechanismApiMock.adopt).toHaveBeenCalledWith('deepseek'));
  await waitFor(() => expect(mechanismApiMock.matrixReport).toHaveBeenCalledTimes(2));
});

it('加载评测矩阵失败时提示错误', async () => {
  const spy = vi.spyOn(message, 'error').mockImplementation(() => ({}) as never);
  mechanismApiMock.matrixReport.mockRejectedValue(new Error('boom'));
  renderWorkbench();
  await waitFor(() => expect(spy).toHaveBeenCalled());
  spy.mockRestore();
});
