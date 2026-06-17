import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import type { ReactNode } from 'react';
import { BrowserRouter } from 'react-router-dom';

import { ChatStreamProvider } from '@/modules/agent';
import { AuthProvider } from '@/modules/iam';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});

interface AppProvidersProps {
  children: ReactNode;
}

export const AppProviders = ({ children }: AppProvidersProps) => (
  <ConfigProvider locale={zhCN}>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <ChatStreamProvider>{children}</ChatStreamProvider>
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </ConfigProvider>
);

export default AppProviders;
