import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

const routesSource = (mod: 'llm' | 'iam' | 'parameters'): string =>
  readFileSync(path.resolve(import.meta.dirname, '../../modules', mod, 'routes.tsx'), 'utf8');

describe('平台管理路由：member 可读、编辑按 minRole 置灰', () => {
  it('模型管理 /models 去 requiredRole 且包 system_admin Gate', () => {
    const src = routesSource('llm');
    expect(src).not.toContain('requiredRole');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
  });

  it('全局租户 /admin/tenants 包 system_admin Gate', () => {
    const src = routesSource('iam');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
    expect(src).toContain('<TenantsListPage />');
  });

  it('平台管理员 /admin/admins 包 global_admin Gate', () => {
    expect(routesSource('iam')).toContain('<PlatformAdminGate minRole="global_admin">');
  });

  it('平台参数 /admin/settings 去 requiredRole 且包 system_admin Gate', () => {
    const src = routesSource('parameters');
    expect(src).not.toContain('requiredRole');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
  });

  it('审计日志 /admin/audit 不加 Gate（页面本身无写控件），仅 iam 两条管理路由被 Gate 包裹', () => {
    const src = routesSource('iam');
    expect(src).toContain('<PlatformAuditPage />');
    expect(src.match(/<PlatformAdminGate/g)?.length).toBe(2);
  });

  it('全部平台管理路由不再使用 requiredRole 守卫', () => {
    for (const mod of ['llm', 'iam', 'parameters'] as const) {
      expect(routesSource(mod)).not.toMatch(/requiredRole=/);
    }
  });
});
