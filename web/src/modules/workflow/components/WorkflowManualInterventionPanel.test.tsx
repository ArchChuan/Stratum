import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { WorkflowManualInterventionPanel } from './WorkflowManualInterventionPanel';

vi.mock('../api/workflow.api', () => ({
  workflowApi: { resolveWorkflowManualIntervention: vi.fn() },
}));

describe('WorkflowManualInterventionPanel', () => {
  beforeEach(() => vi.clearAllMocks());

  it('submits the selected resolution after explicit confirmation', async () => {
    vi.mocked(workflowApi.resolveWorkflowManualIntervention).mockResolvedValue({ status: 'resolved' });
    render(<WorkflowManualInterventionPanel
      intent={{
        id: 'effect-1', run_id: 'run-1', node_id: 'node-1', attempt_id: 'attempt-1',
        run_generation: 3, effect_class: 'non_idempotent', status: 'unknown',
        reason: 'unknown result', output_summary: '',
      }}
      generation={3}
      onChanged={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('处置结果摘要'), { target: { value: '人工确认成功' } });
    fireEvent.click(screen.getByRole('button', { name: '标记成功' }));
    fireEvent.click(await screen.findByRole('button', { name: /确\s*认/ }));

    await waitFor(() => expect(workflowApi.resolveWorkflowManualIntervention).toHaveBeenCalledWith(
      'run-1', 'effect-1',
      { expected_generation: 3, action: 'mark_succeeded', output_summary: '人工确认成功' },
    ));
  });
});
