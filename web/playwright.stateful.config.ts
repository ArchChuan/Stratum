import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  testMatch: 'system-stateful.spec.ts',
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  timeout: 15 * 60_000,
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env.E2E_WEB_URL ?? 'http://127.0.0.1:15173',
    headless: true,
    screenshot: 'off',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  reporter: [['list']],
});
