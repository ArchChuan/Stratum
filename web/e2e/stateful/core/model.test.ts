import { describe, expect, it } from 'vitest';

import {
  addRun, capabilityDomainsForPacks, createSystemModel, reconcileCapabilities, updateWorkflowRevision,
} from './model';

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

  it('reconciles manifest capabilities by browser action ID', () => {
    const result = reconcileCapabilities([
      {
        id: 'dashboard.route.overview',
        domain: 'dashboard',
        browser_action_id: 'dashboard.summary.refresh',
      },
    ], new Set(['dashboard.summary.refresh']));

    expect(result).toEqual({
      capabilities: [{ id: 'dashboard.route.overview', status: 'passed' }],
      unverifiedCapabilities: [],
    });
  });

  it('maps composite packs to the manifest domains they exercise', () => {
    expect(capabilityDomainsForPacks(['agent-context', 'agent-skill-mcp', 'evaluation-promotion']))
      .toEqual(new Set(['agent', 'knowledge', 'memory', 'skill', 'mcp', 'evaluation']));
  });
});
