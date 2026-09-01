import { Alert } from 'antd';
import { createContext, useContext, type ReactNode } from 'react';

import { usePlatformRole } from '../hooks/usePlatformRole';

interface PlatformAdminGateProps {
  /** 可编辑所需的最低平台角色：'system_admin' | 'global_admin'。 */
  minRole: 'system_admin' | 'global_admin';
  children: ReactNode;
}

// 默认 canEdit=true 与改造前行为一致（未包裹 Gate 的页面仍可编辑，向后兼容既有
// 页面测试）。本组件只控制 UI 可用性，真正的写权限由后端中间件强制（fail-closed）；
// 生产路由必须用 PlatformAdminGate 包裹，由路由级源码测试守护（见 routes 测试）。
const PlatformAdminContext = createContext<{ canEdit: boolean }>({ canEdit: true });

/** 读取当前页面是否可编辑；须在 PlatformAdminGate 内使用（默认 true）。 */
export const usePlatformAdminCanEdit = (): boolean => useContext(PlatformAdminContext).canEdit;

export const PlatformAdminGate = ({ minRole, children }: PlatformAdminGateProps) => {
  const { hasPlatformRole } = usePlatformRole();
  const canEdit = hasPlatformRole(minRole);
  return (
    <PlatformAdminContext.Provider value={{ canEdit }}>
      {!canEdit && (
        <Alert
          type="info"
          showIcon
          message="只读模式"
          description="您当前为只读模式，仅平台管理员可编辑本页内容"
          style={{ marginBottom: 16 }}
        />
      )}
      {children}
    </PlatformAdminContext.Provider>
  );
};

export default PlatformAdminGate;
