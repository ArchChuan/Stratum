import type { EvidenceSummary, RandomSource, StatefulAction } from './types';

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

export const serializeScheduleDiagnostics = <M>(result: ScheduleResult<M>): string => JSON.stringify({
  actionSequence: result.actionSequence,
  cycles: result.cycles,
  evidence: result.evidence,
  stopReason: result.stopReason,
} satisfies ScheduleDiagnostics);

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
    if (
      !result.evidence.reconciled
      || result.evidence.ui <= 0
      || result.evidence.http <= 0
      || result.evidence.database <= 0
    ) {
      throw new Error(`action ${selected.id} returned unreconciled evidence`);
    }
    model = result.next;
    evidence.push(result.evidence);
    actionSequence.push(selected.id);
    cycles += 1;
  }

  return { model, evidence, actionSequence, cycles, stopReason: 'cycles' };
};
