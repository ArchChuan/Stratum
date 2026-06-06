-- 回滚：将 ArchChuan 在默认租户中的角色降回 member
UPDATE public.tenant_members tm
SET role = 'member'
FROM public.users u
JOIN public.tenants t ON t.is_default = true
WHERE tm.user_id = u.id
  AND tm.tenant_id = t.id
  AND lower(u.github_login) = lower('ArchChuan');
