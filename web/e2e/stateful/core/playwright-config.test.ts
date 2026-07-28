import { describe, expect, it } from 'vitest';

import config from '../../../playwright.stateful.config';

describe('stateful Playwright security boundaries', () => {
  it('does not persist credential-bearing traces and bounds browser waits', () => {
    expect(config.use?.trace).toBe('off');
    expect(config.use?.screenshot).toBe('off');
    expect(config.use?.actionTimeout).toBeGreaterThan(0);
    expect(config.use?.navigationTimeout).toBeGreaterThan(0);
  });
});
