-- 平台级 HTTP 请求审计废弃(停写删表)。resource_change_audits(tenant schema)
-- 为唯一审计源。历史数据不可回滚，删除前已确认无合规保留需求。
DROP TABLE IF EXISTS public.audit_events;
