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
        colorPrimary: '#2563eb',
        borderRadius: 8,
        borderRadiusLG: 12,
        // 字体：拉丁/数字优先系统 UI 字体（Windows Segoe UI、macOS SF Pro），中文按平台回退。
        // 修复 #321：Inter/PingFang/HarmonyOS 在 Windows 全缺失 → 全站(含拉丁/数字)落到微软雅黑，
        // DirectWrite 下微软雅黑拉丁字形渲染发虚且光栅化慢，页面切换首帧延迟产生残影。
        // CSS 逐字符 fallback：拉丁走 Segoe UI/SF Pro，中文自动走微软雅黑/PingFang。
        fontFamily:
          'Inter, -apple-system, Segoe UI, PingFang SC, HarmonyOS Sans SC, Microsoft YaHei, Noto Sans CJK SC, Roboto, sans-serif',
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
          boxShadowSecondary: '0 2px 6px rgba(37, 99, 235, 0.28)',
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
