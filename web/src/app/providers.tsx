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
        // 字体：数字/英文 Inter 优先，中文按平台回退最优系统字体（Avenir 仅 macOS，缺失时会落到 Arial 渲染粗笨）
        fontFamily:
          'Inter, PingFang SC, HarmonyOS Sans SC, Microsoft YaHei, Noto Sans CJK SC, -apple-system, Segoe UI, Roboto, sans-serif',
        // 控件高度 32→36，缓解 14px 字号局促感（SM 24→28、LG 40→44 配套缩放）
        controlHeight: 36,
        controlHeightSM: 28,
        controlHeightLG: 44,
        // 边框 #d9d9d9 → 冷灰细描边，弱化线框廉价感
        colorBorder: '#d4d4d8',
        colorBorderSecondary: '#ececf1',
        fontWeightStrong: 600,
        // 控件轻投影 + 下拉面板投影，建立层叠层次
        boxShadow: '0 1px 2px rgba(0, 0, 0, 0.05)',
        boxShadowSecondary: '0 4px 12px rgba(0, 0, 0, 0.08)',
      },
      components: {
        Button: {
          // 默认按钮轻投影、主按钮主色投影，三态更有"按得下去"的层次
          boxShadow: '0 1px 2px rgba(0, 0, 0, 0.05)',
          boxShadowSecondary: '0 2px 6px rgba(22, 119, 255, 0.28)',
          fontWeight: 500,
        },
        Card: {
          // 卡片统一轻浮起，与纯线框区分层级
          boxShadow: '0 1px 3px rgba(0, 0, 0, 0.04)',
        },
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
