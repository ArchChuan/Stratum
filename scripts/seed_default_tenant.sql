-- 种子脚本：创建默认租户，将所有现有用户加入默认租户，删除非默认租户
-- 使用固定 UUID 确保幂等性
-- 运行方式: psql $DATABASE_URL -f scripts/seed_default_tenant.sql

BEGIN;

-- 1. 确保 is_default 列存在（migration 未运行时的保险措施）
ALTER TABLE public.tenants
  ADD COLUMN IF NOT EXISTS is_default BOOL NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_is_default
  ON public.tenants (is_default)
  WHERE is_default = true;

-- 2. 插入默认租户（固定 UUID，幂等）
INSERT INTO public.tenants (id, name, slug, plan, status, settings, is_default)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  '默认租户',
  'default',
  'free',
  'active',
  '{}',
  true
)
ON CONFLICT (id) DO UPDATE
  SET name       = EXCLUDED.name,
      slug       = EXCLUDED.slug,
      is_default = true,
      updated_at = now();

-- 3. 将所有现有用户加入默认租户（role=member，跳过已有成员）
INSERT INTO public.tenant_members (tenant_id, user_id, role)
SELECT '00000000-0000-0000-0000-000000000001', u.id, 'member'
FROM public.users u
ON CONFLICT (tenant_id, user_id) DO NOTHING;

-- 4. 删除非默认租户（先删成员记录，再删租户）
DELETE FROM public.tenant_members
WHERE tenant_id IN (
  SELECT id FROM public.tenants
  WHERE is_default = false AND id != '00000000-0000-0000-0000-000000000001'
);

DELETE FROM public.tenants
WHERE is_default = false
  AND id != '00000000-0000-0000-0000-000000000001';

COMMIT;
