import type { RandomSource } from './types';

export const createSeededRandom = (seed: number): RandomSource => {
  let state = seed >>> 0;
  return {
    nextInt: (maxExclusive: number) => {
      if (!Number.isSafeInteger(maxExclusive) || maxExclusive <= 0) {
        throw new Error('maxExclusive must be a positive safe integer');
      }
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
      return Math.floor((state / 0x1_0000_0000) * maxExclusive);
    },
  };
};
