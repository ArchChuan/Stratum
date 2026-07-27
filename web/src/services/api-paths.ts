import systemE2ESurface from './e2e-surface.json';

// Audit registry for user-visible writes. Keep templates aligned with domain API modules.
export const MUTATING_API_PATHS = systemE2ESurface.mutations;
