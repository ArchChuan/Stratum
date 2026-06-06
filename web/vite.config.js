import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

// https://vitejs.dev/config/
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
      host: '0.0.0.0',       // 容器内也能访问
      port: parseInt(env.VITE_PORT || '3002'),
      open: mode === 'development' && !env.CI,
      proxy: {
        // /auth/callback is a frontend React route — must NOT be proxied to backend.
        // Match only the backend auth API endpoints explicitly.
        '^/auth/(github|register|refresh|logout|me)': { target: apiTarget, changeOrigin: true, cookieDomainRewrite: { '*': '' } },
        '/health': { target: apiTarget, changeOrigin: true },
        '/skills': { target: apiTarget, changeOrigin: true, bypass: (req) => req.headers.accept?.includes('text/html') ? '/index.html' : undefined },
        '/agents': { target: apiTarget, changeOrigin: true, bypass: (req) => req.headers.accept?.includes('text/html') ? '/index.html' : undefined },
        '/memory': { target: apiTarget, changeOrigin: true, bypass: (req) => req.headers.accept?.includes('text/html') ? '/index.html' : undefined },
        '/tenant': { target: apiTarget, changeOrigin: true, bypass: (req) => req.headers.accept?.includes('text/html') ? '/index.html' : undefined },
        '/admin': { target: apiTarget, changeOrigin: true, bypass: (req) => req.headers.accept?.includes('text/html') ? '/index.html' : undefined },
        '/metrics': { target: apiTarget, changeOrigin: true },
        '/api': { target: apiTarget, changeOrigin: true },
      },
    },

    build: {
      outDir: 'dist',
      assetsDir: 'assets',
      sourcemap: mode !== 'production', // prod 关闭 source map
      rollupOptions: {
        output: {
          manualChunks: {
            vendor: ['react', 'react-dom', 'react-router-dom'],
            antd: ['antd', '@ant-design/icons'],
          },
        },
      },
    },

    // 开发模式开启 source map 以支持 debug
    esbuild: {
      sourcemap: isDev,
    },
  };
});
