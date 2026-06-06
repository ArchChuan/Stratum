-- 将 GitHub 登录名为 ArchChuan 的用户在默认租户中的角色升为 owner
-- 其余默认租户成员保持 member（不变）
UPDATE public.tenant_members tm
SET role = 'owner'
FROM public.users u
JOIN public.tenants t ON t.is_default = true
WHERE tm.user_id = u.id
  AND tm.tenant_id = t.id
  AND lower(u.github_login) = lower('ArchChuan');
