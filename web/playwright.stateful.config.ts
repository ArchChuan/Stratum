import { defineConfig, devices } from '@playwright/test';

const MAX_STATEFUL_DURATION_MS = 14_400_000;
const STATEFUL_CLEANUP_GRACE_MS = 900_000;

export default defineConfig({
  testDir: './e2e',
  testMatch: 'system-stateful.spec.ts',
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  timeout: MAX_STATEFUL_DURATION_MS + STATEFUL_CLEANUP_GRACE_MS,
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env.E2E_WEB_URL ?? 'http://127.0.0.1:15173',
    headless: true,
    actionTimeout: 20_000,
    navigationTimeout: 30_000,
    screenshot: 'off',
    trace: 'off',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  reporter: [['list']],
});
