import { describe, expect, it } from 'vitest';

import { addRun, createSystemModel, updateWorkflowRevision } from './model';

describe('system state model', () => {
  it('rejects stale workflow revisions', () => {
    const model = createSystemModel();
    const withRevision = updateWorkflowRevision(model, 'workflow-1', 2);

    expect(() => updateWorkflowRevision(withRevision, 'workflow-1', 2)).toThrow('stale workflow revision');
  });

  it('rejects assigning a run to a different owner', () => {
    const model = addRun(createSystemModel(), { id: 'run-1', workflowId: 'workflow-1', ownerId: 'member-a' });

    expect(() => addRun(model, { id: 'run-1', workflowId: 'workflow-1', ownerId: 'member-b' }))
      .toThrow('run ownership cannot change');
  });
});
