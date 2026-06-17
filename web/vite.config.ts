import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const apiTarget = env.VITE_API_BASE_URL || 'http://localhost:8080';
  const isDev = command === 'serve';

  return {
    plugins: [react()],

    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },

    server: {
      host: '0.0.0.0',
      port: parseInt(env.VITE_PORT || '3002'),
      open: mode === 'development' && !env.CI,
      // 不配置 proxy：axios baseURL 已是后端绝对地址（VITE_API_BASE_URL），
      // 直接打后端不走 vite proxy。如果在这里配置前缀 proxy（如 '/knowledge'），
      // vite 会把浏览器刷新时的 HTML 页面请求也转给后端，导致 SPA 路由 404。
    },

    build: {
      sourcemap: isDev || mode === 'staging',
      rollupOptions: {
        output: {
          manualChunks: {
            antd: ['antd', '@ant-design/icons'],
            react: ['react', 'react-dom', 'react-router-dom'],
          },
        },
      },
    },
  };
});
