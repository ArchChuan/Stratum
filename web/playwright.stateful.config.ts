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
  // 共享开发机 soak（多 worktree claude 会话 + lint/semgrep 并发，load 常驻 40）
  // 下后端 latency 实测可尖峰至 10s+（租户级联删除 10003ms），actionTimeout 默认
  // 20s 会被压穿；逐 pack 加显式 timeout 是打地鼠（iam/operation-gate/mechanism 连中）。
  // 系统性放宽等待预算：动作 45s、expect 断言 15s，一次覆盖所有 pack。
  // 不松断言正确性，只放宽预算（隔离跑 ~4s/操作，45s 留 ~10x 余量）。
  expect: { timeout: 15_000 },
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env.E2E_WEB_URL ?? 'http://127.0.0.1:15173',
    headless: true,
    actionTimeout: 45_000,
    navigationTimeout: 30_000,
    screenshot: 'off',
    trace: 'off',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  reporter: [['list']],
});
