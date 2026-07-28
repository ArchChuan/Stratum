import type { EvidenceSummary, RandomSource, StatefulAction } from './types';

import { createSeededRandom } from './random';

export interface ScheduleOptions {
  random: RandomSource;
  maxCycles: number;
  deadlineMs: number;
  now: () => number;
}

export interface ScheduleResult<M> {
  model: M;
  evidence: EvidenceSummary[];
  actionSequence: string[];
  cycles: number;
  stopReason: 'cycles' | 'deadline' | 'no_enabled_actions';
}

export type ScheduleDiagnostics = Omit<ScheduleResult<never>, 'model'>;

export interface AcceptanceScheduleOptions<P extends string> {
  mode: 'short' | 'soak';
  packs: P[];
  durationSeconds: number;
  maxCycles: number;
  seed: number;
  startedAtMs: number;
  now: () => number;
  execute: (pack: P) => Promise<EvidenceSummary>;
}

export interface AcceptanceScheduleResult<P extends string> {
  requiredPacks: P[];
  repeatedPacks: P[];
  stopReason: 'required_pass' | ScheduleResult<unknown>['stopReason'];
}

export const serializeScheduleDiagnostics = <M>(result: ScheduleResult<M>): string => JSON.stringify({
  actionSequence: result.actionSequence,
  cycles: result.cycles,
  evidence: result.evidence,
  stopReason: result.stopReason,
} satisfies ScheduleDiagnostics);

export const executeAcceptanceSchedule = async <P extends string>(
  options: AcceptanceScheduleOptions<P>,
): Promise<AcceptanceScheduleResult<P>> => {
  const requiredPacks: P[] = [];
  for (const pack of options.packs) {
    assertReconciledEvidence(pack, await options.execute(pack));
    requiredPacks.push(pack);
  }
  if (options.mode === 'short') return { requiredPacks, repeatedPacks: [], stopReason: 'required_pass' };

  const actions: StatefulAction<number, undefined>[] = options.packs.map((pack) => ({
    id: pack,
    enabled: () => true,
    run: async (_context, model) => ({ next: model + 1, evidence: await options.execute(pack) }),
  }));
  const scheduled = await runStatefulSchedule(actions, undefined, 0, {
    random: createSeededRandom(options.seed),
    maxCycles: options.maxCycles,
    deadlineMs: options.startedAtMs + options.durationSeconds * 1_000,
    now: options.now,
  });
  return {
    requiredPacks,
    repeatedPacks: scheduled.actionSequence as P[],
    stopReason: scheduled.stopReason,
  };
};

export const runStatefulSchedule = async <M, C>(
  actions: StatefulAction<M, C>[],
  context: C,
  initialModel: M,
  options: ScheduleOptions,
): Promise<ScheduleResult<M>> => {
  if (!Number.isSafeInteger(options.maxCycles) || options.maxCycles <= 0) {
    throw new Error('maxCycles must be a positive safe integer');
  }
  let model = initialModel;
  const evidence: EvidenceSummary[] = [];
  const actionSequence: string[] = [];
  let cycles = 0;

  while (cycles < options.maxCycles) {
    if (options.now() >= options.deadlineMs) {
      return { model, evidence, actionSequence, cycles, stopReason: 'deadline' };
    }
    const enabled = actions.filter((candidate) => candidate.enabled(model));
    if (enabled.length === 0) {
      return { model, evidence, actionSequence, cycles, stopReason: 'no_enabled_actions' };
    }
    const selected = enabled[options.random.nextInt(enabled.length)];
    const result = await selected.run(context, model);
    assertReconciledEvidence(selected.id, result.evidence);
    model = result.next;
    evidence.push(result.evidence);
    actionSequence.push(selected.id);
    cycles += 1;
  }

  return { model, evidence, actionSequence, cycles, stopReason: 'cycles' };
};

const assertReconciledEvidence = (actionID: string, evidence: EvidenceSummary): void => {
  if (!evidence.reconciled || evidence.ui <= 0 || evidence.http <= 0 || evidence.database <= 0) {
    throw new Error(`action ${actionID} returned unreconciled evidence`);
  }
};
