import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import type { ReactNode } from 'react';
import { BrowserRouter } from 'react-router-dom';

import { ChatStreamProvider } from '@/modules/agent';
import { AuthProvider } from '@/modules/iam';

interface AppProvidersProps {
  children: ReactNode;
}

export const AppProviders = ({ children }: AppProvidersProps) => (
  <ConfigProvider locale={zhCN}>
    <BrowserRouter>
      <AuthProvider>
        <ChatStreamProvider>{children}</ChatStreamProvider>
      </AuthProvider>
    </BrowserRouter>
  </ConfigProvider>
);

export default AppProviders;
