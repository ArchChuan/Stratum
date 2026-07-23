import { expect, test } from './fixtures';

test('MCP evidence comes from a real encrypted revision and network tool call', async ({ adminApi, manifest }) => {
  const evidence = manifest.liveEvidence.mcp;
  expect(evidence.toolCalls).toBe(1);
  expect(evidence.encryptedPayloadVerified).toBe(true);
  const job = await adminApi.get(`/evaluations/jobs/${evidence.jobId}`);
  expect(job.status()).toBe(200);
  expect(await job.json()).toMatchObject({ status: 'succeeded', result_id: evidence.runId });
  const run = await adminApi.get(`/evaluations/runs/${evidence.runId}`);
  expect(run.status()).toBe(200);
  expect(await run.json()).toMatchObject({ passed: true, resource: {
    kind: 'mcp', resource_id: evidence.serverId, revision_id: evidence.revisionId,
  } });
});

test('Agent provider failure is recorded and the same stable revision recovers', async ({ adminApi, manifest, scanSafe }) => {
  const evidence = manifest.liveEvidence.agent;
  expect(evidence.toolTraces).toBeGreaterThan(0);
  expect(evidence.traceEvents).toBeGreaterThan(0);
  expect(evidence.tokens).toBeGreaterThan(0);
  await scanSafe(evidence.failureError);

  const failureJob = await adminApi.get(`/evaluations/jobs/${evidence.failureJobId}`);
  expect(failureJob.status()).toBe(200);
  expect(await failureJob.json()).toMatchObject({ status: 'succeeded', result_id: evidence.failureRunId });
  const failureRun = await adminApi.get(`/evaluations/runs/${evidence.failureRunId}`);
  expect(failureRun.status()).toBe(200);
  expect(await failureRun.json()).toMatchObject({ passed: false, resource: {
    kind: 'agent', resource_id: evidence.resourceId, revision_id: evidence.revisionId,
  }, results: [{ passed: false }] });

  const recoveryJob = await adminApi.get(`/evaluations/jobs/${evidence.recoveryJobId}`);
  expect(recoveryJob.status()).toBe(200);
  expect(await recoveryJob.json()).toMatchObject({ status: 'succeeded', result_id: evidence.recoveryRunId });
  const recoveryRun = await adminApi.get(`/evaluations/runs/${evidence.recoveryRunId}`);
  expect(recoveryRun.status()).toBe(200);
  expect(await recoveryRun.json()).toMatchObject({ passed: true, resource: {
    kind: 'agent', resource_id: evidence.resourceId, revision_id: evidence.revisionId,
  }, results: [{ passed: true, actual: 'bounded-agent-result' }] });
});

test('Skill evidence executes the exact published revision through its bound Agent', async ({ adminApi, manifest }) => {
  const evidence = manifest.liveEvidence.skill;
  expect(evidence.traceId).not.toBe('');
  expect(evidence.tokens).toBeGreaterThan(0);
  expect(evidence.llmRequests).toBe(1);
  const job = await adminApi.get(`/evaluations/jobs/${evidence.jobId}`);
  expect(job.status()).toBe(200);
  expect(await job.json()).toMatchObject({ status: 'succeeded', result_id: evidence.runId });
  const run = await adminApi.get(`/evaluations/runs/${evidence.runId}`);
  expect(run.status()).toBe(200);
  expect(await run.json()).toMatchObject({ passed: true, resource: {
    kind: 'skill', resource_id: evidence.resourceId, revision_id: evidence.revisionId,
  }, results: [{ passed: true, actual: 'bounded-agent-result', trace_id: evidence.traceId }] });
});

test('Knowledge evidence preserves document citation through outage and recovery', async ({ adminApi, manifest, scanSafe }) => {
  const evidence = manifest.liveEvidence.knowledge;
  expect(evidence.chunkIndex).toBe(0);
  await scanSafe(evidence.failureError);
  const success = await adminApi.get(`/evaluations/runs/${evidence.runId}`);
  expect(success.status()).toBe(200);
  expect(await success.json()).toMatchObject({ passed: true, resource: {
    kind: 'knowledge', resource_id: evidence.resourceId, revision_id: evidence.revisionId,
  }, results: [{ passed: true, actual: { relevant: true, citation_correct: true,
    retrieved_document_ids: [evidence.documentId] } }] });
  const failure = await adminApi.get(`/evaluations/runs/${evidence.failureRunId}`);
  expect(failure.status()).toBe(200);
  expect(await failure.json()).toMatchObject({ passed: false, resource: { revision_id: evidence.revisionId },
    results: [{ passed: false }] });
  const recovery = await adminApi.get(`/evaluations/runs/${evidence.recoveryRunId}`);
  expect(recovery.status()).toBe(200);
  expect(await recovery.json()).toMatchObject({ passed: true, resource: { revision_id: evidence.revisionId },
    results: [{ passed: true, actual: { retrieved_document_ids: [evidence.documentId] } }] });
});

test('member reads but cannot issue administrator commands', async ({ memberApi, manifest }) => {
  const experiment = manifest.resources.skill;
  const read = await memberApi.get('/evaluations/overview');
  expect(read.status()).toBe(200);
  const command = await memberApi.post(`/evaluations/experiments/${experiment.experimentId}/promote`, {
    data: { reason: 'must fail', idempotency_key: manifest.ids.memberDenied, expected_state_version: 1 },
  });
  expect(command.status()).toBe(403);
});

test('duplicate idempotency is stable and recommendation never auto-promotes', async ({ adminApi, db, manifest }) => {
  const resource = manifest.resources.agent;
  const before = await db.experiment(resource.experimentId);
  expect(before.recommendation).toBe('promote');
  expect(before.status).not.toBe('promoted');
  const first = await adminApi.post(`/evaluations/experiments/${resource.experimentId}/pause`, {
    data: { reason: 'E2E duplicate', idempotency_key: manifest.ids.duplicate, expected_state_version: before.stateVersion },
  });
  const second = await adminApi.post(`/evaluations/experiments/${resource.experimentId}/pause`, {
    data: { reason: 'E2E duplicate', idempotency_key: manifest.ids.duplicate, expected_state_version: before.stateVersion },
  });
  expect(first.status()).toBe(200);
  expect(second.status()).toBe(200);
  expect(await second.json()).toEqual(await first.json());
});

test('failure evidence keeps stable serving and redacts sensitive material', async ({ adminApi, db, manifest, scanSafe }) => {
  for (const scenario of manifest.failureScenarios) {
    const response = await adminApi.get(`/evaluations/runs?resource_id=${scenario.resourceId}`);
    expect(response.status()).toBe(200);
    await scanSafe(JSON.stringify(await response.json()));
    const deployment = await db.deployment(scenario.resourceKind, scenario.resourceId);
    expect(deployment.stableRevision).toBe(scenario.stableRevision);
  }
  await scanSafe(await db.evidenceProjection());
});

test('promotion_evidence is obtained by a real SQL projection', async ({ db }) => {
  const evidence = await db.promotionEvidence();
  expect(evidence.length).toBeGreaterThan(0);
  expect(evidence.every((item) => typeof item.eligible === 'boolean')).toBe(true);
});
