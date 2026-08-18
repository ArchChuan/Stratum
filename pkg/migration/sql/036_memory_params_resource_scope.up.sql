-- 036: memory.* 参数改为资源 scope，绑定到 agent 资源。
-- registry 中 memory.* 由 ScopePlatform 改为 ScopeResource，值存 agents.parameters
-- (per-agent)。平台层仍可为资源键存储默认值；本迁移清理的是此前语义下的
-- 存量孤儿行。DELETE 幂等，对旧代码安全(旧代码读这些键时该表已无行 →
-- 回落 registry 默认，与删除前无平台行时行为一致)。

DELETE FROM public.platform_settings WHERE key LIKE 'memory.%';
