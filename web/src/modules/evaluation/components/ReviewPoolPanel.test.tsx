import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, vi } from 'vitest';

import ReviewPoolPanel from './ReviewPoolPanel';

const mocks = vi.hoisted(() => ({
  listReviewItems: vi.fn(),
  getReviewItem: vi.fn(),
  decideReviewItem: vi.fn(),
  deleteReviewItem: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));
vi.mock('../services/review', () => ({
  listReviewItems: mocks.listReviewItems,
  getReviewItem: mocks.getReviewItem,
  decideReviewItem: mocks.decideReviewItem,
  deleteReviewItem: mocks.deleteReviewItem,
}));
vi.mock('antd', async () => ({ ...(await vi.importActual<typeof import('antd')>('antd')),
  message: { success: mocks.success, error: mocks.error },
}));

const pendingItem = {
  id: 'review-1',
  source_type: 'observation',
  source_id: 'obs-1',
  run_id: 'run-1',
  trace_id: 'trace-1',
  resource_kind: 'skill',
  resource_id: 'skill-1',
  trigger_reason: 'low_confidence',
  risk_level: 'medium',
  snapshot: { score: 0.42, dimension: 'accuracy' },
  status: 'pending',
  created_at: '2026-08-01T00:00:00Z',
};

describe('ReviewPoolPanel', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listReviewItems.mockResolvedValue({ items: [], total: 0 });
  });

  it('renders pending review items with readable labels', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel canDelete={() => false} />);

    expect(await screen.findByText('观测')).toBeInTheDocument();
    expect(screen.getByText('低置信度')).toBeInTheDocument();
    expect(screen.getByText('skill:skill-1')).toBeInTheDocument();
    expect(screen.getByText('待评审')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '详 情' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '评 审' })).toBeInTheDocument();
    await waitFor(() => expect(mocks.listReviewItems).toHaveBeenCalledWith({ page: 1, page_size: 10 }));
  });

  it('renders risk priority label from backend', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel canDelete={() => false} />);

    expect(await screen.findByText('中')).toBeInTheDocument();
  });

  it('shows a placeholder for empty case_result resource columns', async () => {
    mocks.listReviewItems.mockResolvedValue({
      items: [{
        id: 'review-2',
        source_type: 'case_result',
        source_id: 'case-1',
        trigger_reason: 'needs_review',
        risk_level: 'medium',
        resource_kind: '',
        resource_id: '',
        snapshot: { input: 'hello' },
        status: 'reviewed',
        human_verdict: 'pass',
        reviewer: 'admin',
        review_reason: '确认通过',
        created_at: '2026-08-01T00:00:00Z',
        reviewed_at: '2026-08-02T00:00:00Z',
      }],
      total: 1,
    });
    render(<ReviewPoolPanel canDelete={() => false} />);

    expect(await screen.findByText('评测集')).toBeInTheDocument();
    expect(screen.getByText('需人工复核')).toBeInTheDocument();
    expect(screen.getByText('-')).toBeInTheDocument();
    expect(screen.getByText('已评审')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '评 审' })).not.toBeInTheDocument();
  });

  it('labels the process_output_conflict trigger reason', async () => {
    mocks.listReviewItems.mockResolvedValue({
      items: [{ ...pendingItem, id: 'review-3', trigger_reason: 'process_output_conflict' }], total: 1,
    });
    render(<ReviewPoolPanel canDelete={() => false} />);

    expect(await screen.findByText('输出通过但过程未通过')).toBeInTheDocument();
  });

  it('opens the detail drawer showing the snapshot', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel canDelete={() => false} />);

    fireEvent.click(await screen.findByRole('button', { name: '详 情' }));

    expect(screen.getByText('评审详情')).toBeInTheDocument();
    expect(screen.getByText(/"score"/)).toBeInTheDocument();
  });

  it('submits a verdict and reason through the controlled decision modal', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    mocks.decideReviewItem.mockResolvedValue({ ...pendingItem, status: 'reviewed', human_verdict: 'pass' });
    render(<ReviewPoolPanel canDelete={() => false} />);

    fireEvent.click(await screen.findByRole('button', { name: '评 审' }));

    fireEvent.mouseDown(await screen.findByRole('combobox', { name: '评审结论' }));
    fireEvent.click(await screen.findByText('通过'));
    fireEvent.change(screen.getByLabelText('评审理由'), { target: { value: '确认无误' } });
    fireEvent.click(screen.getByRole('button', { name: '提交评审' }));

    await waitFor(() => expect(mocks.decideReviewItem).toHaveBeenCalledWith(
      'review-1', { verdict: 'pass', reason: '确认无误' },
    ));
    expect(mocks.success).toHaveBeenCalledWith({ content: '评审已提交', duration: 2 });
  });

  it('keeps the modal open when required fields are missing', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel canDelete={() => false} />);

    fireEvent.click(await screen.findByRole('button', { name: '评 审' }));
    fireEvent.click(screen.getByRole('button', { name: '提交评审' }));

    await waitFor(() => expect(screen.getByText('请选择评审结论')).toBeInTheDocument());
    expect(mocks.decideReviewItem).not.toHaveBeenCalled();
  });

  it('shows delete only when the caller authorizes it and deletes via the service', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    mocks.deleteReviewItem.mockResolvedValue(undefined);
    render(<ReviewPoolPanel canDelete={(item) => item.id === 'review-1'} />);

    // 行内删除按钮（无 type 按钮双字插空格，渲染为「删 除」）
    const rowDelete = await screen.findByRole('button', { name: '删 除' });
    expect(rowDelete).toBeInTheDocument();
    fireEvent.click(rowDelete);
    // Modal.confirm 确认框内点击确定（okText '删除' →「删 除」）触发删除
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: '删 除' }));
    await waitFor(() => expect(mocks.deleteReviewItem).toHaveBeenCalledWith('review-1'));
    expect(mocks.success).toHaveBeenCalledWith({ content: '评审项已删除', duration: 2 });
  });

  it('hides delete for items the caller does not authorize', async () => {
    mocks.listReviewItems.mockResolvedValue({ items: [pendingItem], total: 1 });
    render(<ReviewPoolPanel canDelete={() => false} />);

    expect(await screen.findByRole('button', { name: '评 审' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '删 除' })).not.toBeInTheDocument();
  });
});
