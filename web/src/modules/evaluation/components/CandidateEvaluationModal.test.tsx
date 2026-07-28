import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { CandidateEvaluationModal } from './CandidateEvaluationModal';

describe('CandidateEvaluationModal', () => {
  it('submits the suite revision and closes after success', async () => {
    const onClose = vi.fn();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<CandidateEvaluationModal open onClose={onClose} onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText('Suite Revision ID'), { target: { value: 'suite-revision-1' } });
    fireEvent.click(screen.getByRole('button', { name: '开始评测' }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('suite-revision-1', expect.any(String)));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('reuses the idempotency key when a failed enqueue is retried', async () => {
    const onSubmit = vi.fn().mockRejectedValueOnce(new Error('响应丢失')).mockResolvedValue(undefined);
    render(<CandidateEvaluationModal open onClose={vi.fn()} onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText('Suite Revision ID'), { target: { value: 'suite-revision-1' } });
    fireEvent.click(screen.getByRole('button', { name: '开始评测' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '开始评测' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));

    expect(onSubmit.mock.calls[0][1]).toEqual(expect.any(String));
    expect(onSubmit.mock.calls[0][1]).toBe(onSubmit.mock.calls[1][1]);
  });
});
