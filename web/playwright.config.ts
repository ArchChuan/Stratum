import { defineConfig, devices } from '@playwright/test';

const viewports = [
  { name: 'mobile-320', width: 320, height: 568 },
  { name: 'mobile-375', width: 375, height: 667 },
  { name: 'mobile-390', width: 390, height: 844 },
  { name: 'mobile-430', width: 430, height: 932 },
  { name: 'desktop-1440', width: 1440, height: 900 },
];

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  use: {
    baseURL: 'http://localhost:5173',
    headless: true,
    screenshot: 'off',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 5173',
    url: 'http://localhost:5173',
    reuseExistingServer: false,
    timeout: 120_000,
  },
  projects: viewports.map(({ name, width, height }) => ({
    name,
    use: {
      ...devices['Desktop Chrome'],
      viewport: { width, height },
    },
  })),
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
});
