import { defineConfig, devices } from '@playwright/test';

const viewports = [
  { name: 'mobile-390', width: 390, height: 844 },
  { name: 'desktop-1440', width: 1440, height: 900 },
];

export default defineConfig({
  testDir: './e2e',
  testMatch: 'system-assistant-real.spec.ts',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: process.env.E2E_WEB_URL || 'http://127.0.0.1:5173',
    headless: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: viewports.map(({ name, width, height }) => ({
    name,
    use: {
      ...devices['Desktop Chrome'],
      viewport: { width, height },
    },
  })),
  reporter: [['list']],
});
