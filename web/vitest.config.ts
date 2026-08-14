import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react({ fastRefresh: false })],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    // 限 2 worker：多 worktree 并行跑测试时防止 vitest 吃满 CPU
    // （与 scripts/quality/risk-regression-guard.sh 的 --maxWorkers=2 一致）
    maxWorkers: 2,
    // 异步 RTL UI 测试在共享开发机（多 worktree 并发 + 其他扫描进程）下
    // 单测墙钟可能超过 vitest 默认 5s；给足预算避免负载尖峰触发随机超时。
    // 不松断言，仅放宽时间预算（隔离运行全绿 ~4s，15s 留 3-4x 余量）。
    testTimeout: 15_000,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      include: ['src/modules/**/*.{ts,tsx}', 'src/shared/**/*.{ts,tsx}'],
      exclude: ['**/__tests__/**', '**/*.test.*'],
    },
  },
});
