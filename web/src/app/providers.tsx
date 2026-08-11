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
  <ConfigProvider
    locale={zhCN}
    theme={{
      token: {
        // 主题 token 骨架：控件圆角 8、卡片圆角 12（AntD borderRadiusLG）、主色统一为 AntD default blue
        colorPrimary: '#1677ff',
        borderRadius: 8,
        borderRadiusLG: 12,
      },
    }}
  >
    <BrowserRouter>
      <AuthProvider>
        <ChatStreamProvider>{children}</ChatStreamProvider>
      </AuthProvider>
    </BrowserRouter>
  </ConfigProvider>
);

export default AppProviders;
