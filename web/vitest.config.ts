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
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      include: ['src/modules/**/*.{ts,tsx}', 'src/shared/**/*.{ts,tsx}'],
      exclude: ['**/__tests__/**', '**/*.test.*'],
    },
  },
});
