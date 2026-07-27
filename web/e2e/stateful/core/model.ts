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
