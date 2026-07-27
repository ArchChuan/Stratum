import { describe, expect, it } from 'vitest';

import { createSeededRandom } from './random';
import { runStatefulSchedule, serializeScheduleDiagnostics } from './scheduler';
import type { StatefulAction } from './types';

interface CounterModel { count: number }
type TestContext = { executed: string[] };

const action = (id: string, enabled: (model: CounterModel) => boolean): StatefulAction<CounterModel, TestContext> => ({
  id,
  enabled,
  run: async (context, model) => {
    context.executed.push(id);
    return {
      next: { count: model.count + 1 },
      evidence: { ui: 1, http: 1, database: 1, reconciled: true },
    };
  },
});

describe('stateful scheduler', () => {
  it('replays the same enabled action sequence for the same seed', async () => {
    const actions = [action('first', () => true), action('second', () => true)];
    const execute = async () => {
      const context: TestContext = { executed: [] };
      const result = await runStatefulSchedule(actions, context, { count: 0 }, {
        random: createSeededRandom(42), maxCycles: 8, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
      });
      return serializeScheduleDiagnostics(result);
    };

    const first = await execute();
    expect(first).toBe(await execute());
    expect(JSON.parse(first).actionSequence).toMatchInlineSnapshot(`
      [
        "first",
        "first",
        "second",
        "first",
        "first",
        "first",
        "first",
        "first",
      ]
    `);
    expect(first).not.toMatch(/authorization|cookie|password|private.?key|api.?key|token/i);
  });

  it('allows different seeds to select different valid sequences', async () => {
    const actions = [action('first', () => true), action('second', () => true), action('third', () => true)];
    const execute = async (seed: number) => {
      const context: TestContext = { executed: [] };
      await runStatefulSchedule(actions, context, { count: 0 }, {
        random: createSeededRandom(seed), maxCycles: 8, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
      });
      return context.executed;
    };

    expect(await execute(1)).not.toEqual(await execute(2));
  });

  it('never selects disabled actions', async () => {
    const context: TestContext = { executed: [] };
    await runStatefulSchedule([
      action('disabled', () => false), action('enabled', () => true),
    ], context, { count: 0 }, {
      random: createSeededRandom(7), maxCycles: 4, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
    });

    expect(context.executed).toEqual(['enabled', 'enabled', 'enabled', 'enabled']);
  });

  it('stops between atomic actions when the cycle or time budget expires', async () => {
    let now = 0;
    const timedAction: StatefulAction<CounterModel, TestContext> = {
      ...action('timed', () => true),
      run: async (context, model) => {
        context.executed.push('timed');
        now += 10;
        return { next: { count: model.count + 1 }, evidence: { ui: 1, http: 1, database: 1, reconciled: true } };
      },
    };
    const context: TestContext = { executed: [] };
    const result = await runStatefulSchedule([timedAction], context, { count: 0 }, {
      random: createSeededRandom(1), maxCycles: 10, deadlineMs: 25, now: () => now,
    });

    expect(context.executed).toHaveLength(3);
    expect(result.stopReason).toBe('deadline');
  });

  it('reports cycle and no-enabled-action termination', async () => {
    const context: TestContext = { executed: [] };
    const cycles = await runStatefulSchedule([action('enabled', () => true)], context, { count: 0 }, {
      random: createSeededRandom(1), maxCycles: 1, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
    });
    const none = await runStatefulSchedule([action('disabled', () => false)], context, { count: 0 }, {
      random: createSeededRandom(1), maxCycles: 1, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
    });

    expect(cycles.stopReason).toBe('cycles');
    expect(none.stopReason).toBe('no_enabled_actions');
  });

  it('rejects an action before applying state when evidence is not reconciled', async () => {
    const unreconciled: StatefulAction<CounterModel, TestContext> = {
      id: 'unreconciled', enabled: () => true,
      run: async () => ({ next: { count: 1 }, evidence: { ui: 1, http: 1, database: 1, reconciled: false } }),
    };

    await expect(runStatefulSchedule([unreconciled], { executed: [] }, { count: 0 }, {
      random: createSeededRandom(1), maxCycles: 1, deadlineMs: Number.POSITIVE_INFINITY, now: () => 0,
    })).rejects.toThrow('unreconciled evidence');
  });
});
