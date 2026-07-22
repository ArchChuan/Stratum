import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { vi } from 'vitest';

import { SkillEvaluationPanel } from './SkillEvaluationPanel';

vi.mock('react-router-dom', () => ({
  Link: ({ to, children, ...props }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) => (
    <a href={to} {...props}>{children}</a>
  ),
}));

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

  it('member can open the resource-scoped center without command controls', () => {
    render(<SkillEvaluationPanel skillId="skill with space" stableRevisionId="stable-1" isAdmin={false} />);

    expect(screen.getByRole('link', { name: '打开评测与进化中心' })).toHaveAttribute(
      'href',
      '/evaluations?kind=skill&resource_id=skill%20with%20space',
    );
    expect(screen.queryByRole('button', { name: '创建并发布评测集' })).not.toBeInTheDocument();
  });

  it('admin retains create and run commands with the center link', async () => {
    createSuite.mockResolvedValue({
      suite: { id: 'suite-1', name: '回归评测' },
      revision: { id: 'draft-1', suite_id: 'suite-1', status: 'draft', resource_kind: 'skill', cases: [] },
    });
    publishSuite.mockResolvedValue({
      id: 'revision-1', suite_id: 'suite-1', status: 'published', resource_kind: 'skill', cases: [],
    });
    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="stable-1" isAdmin />);

    expect(screen.getByRole('link', { name: '打开评测与进化中心' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '创建并发布评测集' }));
    expect(await screen.findByRole('button', { name: '运行基线评测' })).toBeInTheDocument();
  });

  it('keeps the published revision warning before publish', () => {
    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="" isAdmin />);
    expect(screen.getByText('请先发布 Skill，再进行评测与优化。')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '打开评测与进化中心' })).not.toBeInTheDocument();
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

  it('保留完整候选与灰度流程并同时提供评测中心入口', async () => {
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
        source: 'llm_rewrite', revision: { kind: 'skill', resource_id: 'skill-1', revision_id: 'canary-1' },
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
    expect(screen.getByRole('link', { name: '打开评测与进化中心' })).toBeInTheDocument();
  });

  it('每次生成候选版本都发送新的幂等键', async () => {
    createSuite.mockResolvedValue({
      suite: { id: 'suite-1', name: '回归评测' },
      revision: { id: 'draft-1', suite_id: 'suite-1', status: 'draft', resource_kind: 'skill', cases: [] },
    });
    publishSuite.mockResolvedValue({
      id: 'revision-1', suite_id: 'suite-1', status: 'published', resource_kind: 'skill', cases: [],
    });
    enqueueRun.mockResolvedValue({ job_id: 'job-stable', status: 'queued' });
    getJob.mockResolvedValue({ job_id: 'job-stable', status: 'succeeded', result_id: 'run-stable' });
    getRun.mockResolvedValue({ id: 'run-stable', passed: true, total_cases: 1, passed_cases: 1, results: [] });
    generateOptimization.mockResolvedValue({
      job: { id: 'optimization-1', status: 'succeeded' },
      candidates: [{
        id: 'candidate-1', optimization_job_id: 'optimization-1', parent_revision_id: 'stable-1',
        source: 'llm_rewrite', revision: { kind: 'skill', resource_id: 'skill-1', revision_id: 'canary-1' },
      }],
    });

    render(<SkillEvaluationPanel skillId="skill-1" stableRevisionId="stable-1" isAdmin />);
    fireEvent.click(screen.getByRole('button', { name: '创建并发布评测集' }));
    fireEvent.click(await screen.findByRole('button', { name: '运行基线评测' }));
    const generateButton = await screen.findByRole('button', { name: '生成候选版本' });

    fireEvent.click(generateButton);
    await waitFor(() => expect(generateOptimization).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(generateButton).toBeEnabled());
    fireEvent.click(generateButton);
    await waitFor(() => expect(generateOptimization).toHaveBeenCalledTimes(2));

    const firstKey = generateOptimization.mock.calls[0][0].idempotencyKey;
    const secondKey = generateOptimization.mock.calls[1][0].idempotencyKey;
    expect(firstKey).toEqual(expect.any(String));
    expect(firstKey).not.toBe('');
    expect(secondKey).toEqual(expect.any(String));
    expect(secondKey).not.toBe(firstKey);
  });
});
