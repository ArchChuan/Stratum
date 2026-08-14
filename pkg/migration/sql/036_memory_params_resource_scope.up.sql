-- 036: memory.* 参数从平台层移除，绑定到 agent 资源。
-- registry 中 memory.* 由 ScopePlatform 改为 ScopeResource，值存 agents.parameters
-- (per-agent)，不再是平台共享默认。resolveOne 无条件读平台层，存量孤儿行会继续
-- 作为资源键的中间层 fallback，必须清理。DELETE 幂等，对旧代码安全(旧代码读这些键
-- 时该表已无行 → 回落 registry 默认，与删除前无平台行时行为一致)。

DELETE FROM public.platform_settings WHERE key LIKE 'memory.%';
