import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import { expect, test } from '@playwright/test';

import { createActorContexts, createGuestActor, type ActorLabel, type BrowserActor } from './stateful/core/actors';
import { createSafePool, deleteGeneratedActors } from './stateful/core/database';
import { acceptanceError, acceptanceErrors } from './stateful/core/errors';
import type { EvidenceRecord } from './stateful/core/evidence';
import { parseRuntimeOptions, type SystemPack } from './stateful/core/runtime';
import { dashboardActions } from './stateful/packs/dashboard';
import { executeIAMPack } from './stateful/packs/iam';
import { executeWorkflowPack } from './stateful/packs/workflow';

interface ManifestCapability { id: string; domain: string }
interface SafeResultItem { id: string; status: 'passed' }

const runtime = parseRuntimeOptions(process.env);
const repositoryRoot = fileURLToPath(new URL('../..', import.meta.url));
const manifestPath = fileURLToPath(new URL('../../test/e2e/stateful/manifest.json', import.meta.url));

const emptyEvidence = (): EvidenceRecord => ({ ui: [], http: [], database: [] });

test('system stateful acceptance', async ({ browser, browserName }) => {
  expect(browserName).toBe('chromium');
  const databaseURL = process.env.TEST_DATABASE_URL;
  const resultsPath = process.env.STATEFUL_E2E_RESULTS_PATH;
  const backendURL = process.env.E2E_API_URL ?? 'http://127.0.0.1:18080';
  const webURL = process.env.E2E_WEB_URL ?? 'http://127.0.0.1:15173';
  if (!databaseURL || !resultsPath) throw new Error('stateful E2E database and results paths are required');

  const pool = await createSafePool(databaseURL);
  const contexts = await createActorContexts(browser);
  const actors = {} as Record<ActorLabel, BrowserActor>;
  const evidence = emptyEvidence();
  const completedPacks: SafeResultItem[] = [];
  const completedActions: string[] = [];
  const startedAt = new Date();
  let cleanupPassed = false;
  let executionError: unknown;
  try {
    for (const label of Object.keys(contexts) as ActorLabel[]) {
      actors[label] = await createGuestActor(contexts[label], webURL, backendURL, pool);
    }
    for (const pack of runtime.packs) {
      await executePack(pack, actors, pool, evidence, webURL, backendURL, completedActions);
      completedPacks.push({ id: pack, status: 'passed' });
    }
  } catch (error) {
    executionError = error;
  }

  let cleanupError: unknown;
  const closeResults = await Promise.allSettled(Object.values(contexts).map(({ context }) => context.close()));
  cleanupError = acceptanceErrors(closeResults.flatMap((result) => (
    result.status === 'rejected' ? [result.reason] : []
  )));
  try {
    await deleteGeneratedActors(pool, Object.values(actors).flatMap(({ userID }) => userID ? [userID] : []));
  } catch (error) {
    cleanupError = acceptanceError(cleanupError, error);
  }
  try {
    await pool.end();
  } catch (error) {
    cleanupError = acceptanceError(cleanupError, error);
  }
  cleanupPassed = cleanupError === undefined;
  const failure = acceptanceError(executionError, cleanupError);
  if (failure) throw failure;

  const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as { capabilities: ManifestCapability[] };
  const selectedDomains = new Set(runtime.packs.filter((pack) => !pack.includes('-')));
  const selectedCapabilities = manifest.capabilities
    .filter(({ domain }) => selectedDomains.has(domain as SystemPack));
  const completedSet = new Set(completedActions);
  const capabilities = selectedCapabilities
    .filter(({ id }) => completedSet.has(id))
    .map(({ id }) => ({ id, status: 'passed' as const }));
  const unverifiedCapabilities = selectedCapabilities
    .filter(({ id }) => !completedSet.has(id))
    .map(({ id }) => id);
  const durationSeconds = Math.max(1, Math.ceil((Date.now() - startedAt.getTime()) / 1000));
  const safeResults = {
    tested_git_parent: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' }).trim(),
    browser: { name: 'chromium', version: browser.version() },
    mode: runtime.mode,
    seed: runtime.seed,
    started_at: startedAt.toISOString(),
    duration_seconds: durationSeconds,
    host_class: 'developer',
    packs: completedPacks,
    capabilities,
    action_count: completedActions.length,
    sequence_digest: createHash('sha256').update(completedActions.join('\0')).digest('hex'),
    evidence: {
      ui: evidence.ui.length,
      http: evidence.http.length,
      database: evidence.database.length,
      reconciled: completedActions.length,
    },
    artifacts: [],
    cleanup: { passed: cleanupPassed, residual_entity_ids: [] },
    unverified_capabilities: unverifiedCapabilities,
    risk_classification: runtime.mode,
    status: unverifiedCapabilities.length === 0 ? 'passed' : 'failed',
  };
  await writeFile(resultsPath, `${JSON.stringify(safeResults, null, 2)}\n`, { mode: 0o600 });
  expect(unverifiedCapabilities, 'every selected manifest capability must execute').toEqual([]);
});

const executePack = async (
  pack: SystemPack,
  actors: Record<ActorLabel, BrowserActor>,
  pool: Awaited<ReturnType<typeof createSafePool>>,
  evidence: EvidenceRecord,
  webURL: string,
  backendURL: string,
  completedActions: string[],
): Promise<void> => {
  if (pack === 'iam') {
    completedActions.push(...await executeIAMPack({ actors, pool, evidence, webURL, backendURL }));
    return;
  }
  if (pack === 'workflow') {
    completedActions.push(...await executeWorkflowPack({ actor: actors.tenantAdmin, pool, evidence, webURL }));
    return;
  }
  if (pack !== 'dashboard') throw new Error(`stateful pack ${pack} is not implemented`);
  const actor = actors.memberA;
  if (!actor.tenantID) throw new Error('member A tenant is unavailable');
  const page = await actor.context.newPage();
  try {
    for (const action of dashboardActions) {
      await action.execute({ page, evidence, pool, tenantID: actor.tenantID, webURL });
      completedActions.push(action.id);
    }
  } finally {
    await page.close();
  }
};
