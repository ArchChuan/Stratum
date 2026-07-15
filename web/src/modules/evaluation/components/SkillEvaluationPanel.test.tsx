import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { vi } from 'vitest';

import { SkillEvaluationPanel } from './SkillEvaluationPanel';

const createSuite = vi.fn();
const publishSuite = vi.fn();
const enqueueRun = vi.fn();
const getJob = vi.fn();
const getRun = vi.fn();
const generateOptimization = vi.fn();
const createExperiment = vi.fn();

vi.mock('../api/evaluation.api', () => ({
  evaluationApi: {
    createSuite: (...args: unknown[]) => createSuite(...args),
    publishSuite: (...args: unknown[]) => publishSuite(...args),
    enqueueRun: (...args: unknown[]) => enqueueRun(...args),
    getJob: (...args: unknown[]) => getJob(...args),
    getRun: (...args: unknown[]) => getRun(...args),
    generateOptimization: (...args: unknown[]) => generateOptimization(...args),
    createExperiment: (...args: unknown[]) => createExperiment(...args),
  },
}));

describe('SkillEvaluationPanel', () => {
  beforeEach(() => {
    createSuite.mockReset();
    publishSuite.mockReset();
    enqueueRun.mockReset();
    getJob.mockReset();
    getRun.mockReset();
    generateOptimization.mockReset();
    createExperiment.mockReset();
  });

  it('创建并发布评测集后展示 revision', async () => {
    createSuite.mockResolvedValue({
      suite: { id: 'suite-1', name: '回归评测' },
      revision: { id: 'draft-1', suite_id: 'suite-1', status: 'draft', resource_kind: 'skill', cases: [] },
    });
    publishSuite.mockResolvedValue({
      id: 'revision-1', suite_id: 'suite-1', status: 'published', resource_kind: 'skill', cases: [],
    });

    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="stable-1" isAdmin />);
    fireEvent.click(screen.getByRole('button', { name: '创建并发布评测集' }));

    await waitFor(() => expect(publishSuite).toHaveBeenCalledWith('suite-1'));
    expect(await screen.findByText(/revision-1/)).toBeInTheDocument();
  });

  it('完成基线评测、候选评测并创建灰度实验', async () => {
    createSuite.mockResolvedValue({
      suite: { id: 'suite-1', name: '回归评测' },
      revision: { id: 'draft-1', suite_id: 'suite-1', status: 'draft', resource_kind: 'skill', cases: [] },
    });
    publishSuite.mockResolvedValue({
      id: 'revision-1', suite_id: 'suite-1', status: 'published', resource_kind: 'skill', cases: [],
    });
    enqueueRun
      .mockResolvedValueOnce({ job_id: 'job-stable', status: 'queued' })
      .mockResolvedValueOnce({ job_id: 'job-canary', status: 'queued' });
    getJob
      .mockResolvedValueOnce({ job_id: 'job-stable', status: 'succeeded', result_id: 'run-stable' })
      .mockResolvedValueOnce({ job_id: 'job-canary', status: 'succeeded', result_id: 'run-canary' });
    getRun
      .mockResolvedValueOnce({ id: 'run-stable', passed: true, total_cases: 1, passed_cases: 1, results: [] })
      .mockResolvedValueOnce({ id: 'run-canary', passed: true, total_cases: 1, passed_cases: 1, results: [] });
    generateOptimization.mockResolvedValue({
      job: { id: 'optimization-1', status: 'succeeded' },
      candidates: [{
        id: 'candidate-1', optimization_job_id: 'optimization-1', parent_revision_id: 'stable-1',
        source: 'parameter_search', revision: { kind: 'skill', resource_id: 'skill-1', revision_id: 'canary-1' },
      }],
    });
    createExperiment.mockResolvedValue({
      experiment: { id: 'experiment-1', status: 'running', stage: 5 },
      deployment: { stable_revision_id: 'stable-1', canary_revision_id: 'canary-1', canary_percent: 5 },
    });

    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="stable-1" isAdmin />);
    fireEvent.click(screen.getByRole('button', { name: '创建并发布评测集' }));
    fireEvent.click(await screen.findByRole('button', { name: '运行基线评测' }));
    expect(await screen.findByText('基线评测：通过（1/1）')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '生成候选版本' }));
    fireEvent.click(await screen.findByRole('button', { name: '运行候选评测' }));
    expect(await screen.findByText('候选评测：通过（1/1）')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '创建 5% 灰度实验' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认创建' }));

    await waitFor(() => expect(createExperiment).toHaveBeenCalled());
    expect(await screen.findByText(/experiment-1/)).toBeInTheDocument();
  });
});
