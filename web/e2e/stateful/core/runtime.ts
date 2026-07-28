export const SYSTEM_PACKS = [
  'dashboard', 'iam', 'workflow', 'agent', 'skill', 'mcp', 'agent-skill-mcp',
  'knowledge', 'memory', 'evaluation', 'agent-context', 'evaluation-promotion',
] as const;

export type SystemPack = typeof SYSTEM_PACKS[number];
export type ExecutionMode = 'short' | 'soak';
export type AcceptanceProfile = 'test' | 'release';

export interface RuntimeOptions {
  mode: ExecutionMode;
  acceptanceProfile?: AcceptanceProfile;
  durationSeconds: number;
  maxCycles: number;
  seed: number;
  packs: SystemPack[];
}

const parsePositiveInteger = (raw: string | undefined, fallback: number, name: string): number => {
  const value = raw === undefined ? fallback : Number(raw);
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${name} must be a positive safe integer`);
  return value;
};

export const parseRuntimeOptions = (environment: NodeJS.ProcessEnv): RuntimeOptions => {
  const mode = environment.STATEFUL_E2E_MODE ?? 'short';
  if (mode !== 'short' && mode !== 'soak') throw new Error(`unsupported stateful E2E mode: ${mode}`);
  const rawProfile = environment.STATEFUL_E2E_PROFILE;
  if (mode === 'short' && rawProfile !== undefined && rawProfile !== '') {
    throw new Error('short mode cannot declare an acceptance profile');
  }
  if (mode === 'soak' && rawProfile !== undefined && rawProfile !== 'test' && rawProfile !== 'release') {
    throw new Error(`unsupported acceptance profile: ${rawProfile}`);
  }
  const acceptanceProfile = mode === 'soak' ? (rawProfile ?? 'test') as AcceptanceProfile : undefined;
  const defaultDuration = acceptanceProfile === 'release' ? 3600 : 600;
  const durationSeconds = parsePositiveInteger(
    environment.STATEFUL_E2E_DURATION_SEC,
    defaultDuration,
    'STATEFUL_E2E_DURATION_SEC',
  );
  if (durationSeconds < 600 || durationSeconds > 14_400) {
    throw new Error('STATEFUL_E2E_DURATION_SEC must be between 600 and 14400');
  }
  if (acceptanceProfile === 'release' && durationSeconds < 3600) {
    throw new Error('STATEFUL_E2E_DURATION_SEC is below release minimum 3600');
  }
  const seed = Number(environment.STATEFUL_E2E_SEED ?? '1');
  if (!Number.isSafeInteger(seed) || seed < 0 || seed > 0xffff_ffff) {
    throw new Error('STATEFUL_E2E_SEED must be an unsigned 32-bit integer');
  }
  const requested = (environment.STATEFUL_E2E_PACKS ?? 'all').split(',').map((pack) => pack.trim());
  const packs = requested.length === 1 && requested[0] === 'all' ? [...SYSTEM_PACKS] : requested;
  if (packs.length === 0 || packs.some((pack) => !SYSTEM_PACKS.includes(pack as SystemPack))) {
    throw new Error(`unknown stateful E2E pack in: ${requested.join(',')}`);
  }
  if (new Set(packs).size !== packs.length) throw new Error('stateful E2E packs must be unique');
  return {
    mode,
    acceptanceProfile,
    durationSeconds,
    maxCycles: parsePositiveInteger(environment.STATEFUL_E2E_MAX_CYCLES, mode === 'short' ? 100 : 10_000, 'STATEFUL_E2E_MAX_CYCLES'),
    seed,
    packs: packs as SystemPack[],
  };
};
