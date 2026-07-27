export interface WorkflowState {
  id: string;
  revision: number;
}

export interface RunState {
  id: string;
  workflowId: string;
  ownerId: string;
}

export interface SystemModel {
  workflows: Record<string, WorkflowState>;
  runs: Record<string, RunState>;
}

export interface ManifestCapability {
  id: string;
  domain: string;
  browser_action_id: string;
}

interface CapabilityReconciliation {
  capabilities: Array<{ id: string; status: 'passed' }>;
  unverifiedCapabilities: string[];
}

export const createSystemModel = (): SystemModel => ({ workflows: {}, runs: {} });

export const updateWorkflowRevision = (model: SystemModel, workflowId: string, revision: number): SystemModel => {
  const current = model.workflows[workflowId];
  if (current && revision <= current.revision) throw new Error('stale workflow revision');
  return {
    ...model,
    workflows: { ...model.workflows, [workflowId]: { id: workflowId, revision } },
  };
};

export const addRun = (model: SystemModel, run: RunState): SystemModel => {
  const current = model.runs[run.id];
  if (current && current.ownerId !== run.ownerId) throw new Error('run ownership cannot change');
  return { ...model, runs: { ...model.runs, [run.id]: { ...run } } };
};

const COMPOSITE_PACK_DOMAINS: Record<string, string[]> = {
  'agent-context': ['agent', 'knowledge', 'memory'],
  'agent-skill-mcp': ['agent', 'skill', 'mcp'],
  'evaluation-promotion': ['evaluation'],
};

export const capabilityDomainsForPacks = (packs: readonly string[]): Set<string> => new Set(
  packs.flatMap((pack) => COMPOSITE_PACK_DOMAINS[pack] ?? [pack]),
);

export const reconcileCapabilities = (
  selectedCapabilities: ManifestCapability[],
  completedActions: ReadonlySet<string>,
): CapabilityReconciliation => ({
  capabilities: selectedCapabilities
    .filter(({ browser_action_id: actionID }) => completedActions.has(actionID))
    .map(({ id }) => ({ id, status: 'passed' })),
  unverifiedCapabilities: selectedCapabilities
    .filter(({ browser_action_id: actionID }) => !completedActions.has(actionID))
    .map(({ id }) => id),
});
