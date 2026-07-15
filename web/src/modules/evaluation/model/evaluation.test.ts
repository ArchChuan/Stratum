import { describe, expect, it } from 'vitest';

import { evaluationJobSchema, optimizationResponseSchema } from './evaluation';

describe('evaluation model', () => {
  it('parses completed job with result id', () => {
    const job = evaluationJobSchema.parse({ job_id: 'job-1', status: 'succeeded', result_id: 'run-1' });
    expect(job.result_id).toBe('run-1');
  });

  it('parses generated candidate revisions', () => {
    const response = optimizationResponseSchema.parse({
      job: { id: 'optimization-1', status: 'succeeded' },
      candidates: [
        {
          id: 'candidate-record-1',
          optimization_job_id: 'optimization-1',
          revision: { kind: 'skill', resource_id: 'skill-1', revision_id: 'candidate-1' },
          parent_revision_id: 'version-1',
          source: 'parameter_search',
        },
      ],
    });
    expect(response.candidates[0].revision.revision_id).toBe('candidate-1');
  });
});
