import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { EvolutionCommandModal } from './EvolutionCommandModal';

describe('EvolutionCommandModal', () => {
  it('exposes optimization, experiment, and feedback commands', () => {
    render(<EvolutionCommandModal open onClose={vi.fn()} onOptimize={vi.fn()} onExperiment={vi.fn()} onFeedback={vi.fn()} />);
    expect(screen.getByRole('tab', { name: '生成优化候选' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '创建金丝雀' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '记录反馈' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '记录反馈' }));
    expect(screen.getByRole('button', { name: '提交反馈' })).toBeInTheDocument();
  });
});
