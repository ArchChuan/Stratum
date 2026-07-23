import { defineConfig, devices } from '../../web/node_modules/@playwright/test/index.js';

export default defineConfig({
  testDir: '.',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_WEB_URL || 'http://127.0.0.1:15173',
    headless: true,
    screenshot: 'off',
    trace: 'off',
    ...devices['Desktop Chrome'],
  },
});
