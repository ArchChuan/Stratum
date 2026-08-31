import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { GenerateCasesModal } from './GenerateCasesModal';

import { EVALUATION_GENERATE_DEFAULT_MAX_CASES } from '@/constants';

describe('GenerateCasesModal', () => {
  it('defaults to balanced sampling and the platform sample limit', async () => {
    const onClose = vi.fn();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<GenerateCasesModal open onClose={onClose} onSubmit={onSubmit} />);

    expect(screen.getByText('均衡采样')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /生\s*成/ }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({
      samplePolicy: 'balanced', maxCases: EVALUATION_GENERATE_DEFAULT_MAX_CASES,
    }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('closes without submitting when cancelled', () => {
    const onClose = vi.fn();
    render(<GenerateCasesModal open onClose={onClose} onSubmit={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: /取\s*消/ }));
    expect(onClose).toHaveBeenCalled();
  });
});
