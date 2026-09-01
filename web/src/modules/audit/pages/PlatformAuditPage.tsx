import { auditApi } from '../api/audit.api';

import { AuditEventsPage } from './AuditEventsPage';

import { PLATFORM_RESOURCE_KIND_OPTIONS } from '@/constants';

// 平台审计页：复用租户审计页的列表/筛选/抽屉能力，数据源切换到平台级
// /admin/audit/platform/events。无写控件，所有登录租户成员只读可见（路由不再用 requiredRole 守卫）。
export const PlatformAuditPage = () => {
  return (
    <AuditEventsPage
      title="平台审计日志"
      description="平台管理下的租户、平台管理员、模型、厂商与平台配置变更记录"
      emptyText="还没有平台操作记录"
      resourceKindOptions={PLATFORM_RESOURCE_KIND_OPTIONS}
      fetchers={{ listEvents: auditApi.listPlatformEvents, getEvent: auditApi.getPlatformEvent }}
    />
  );
};

export default PlatformAuditPage;
