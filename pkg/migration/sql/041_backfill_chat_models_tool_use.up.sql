-- 041: 回填存量 chat 模型 tool_use 能力
-- inferCapabilities 修复前，Discover 写入的 chat 模型能力集为 [chat]（缺 tool_use），
-- 网关 L4 对能力集非空但缺 tool_use 的模型拦截 ReAct 工具调用（lacks tool_use）。
-- 修复只对新 Discover 写入生效；存量模型 capabilities 被 upsertSyncModel 视为
-- 用户可编辑字段而保留，不会随 discover/warm 更新，需一次性回填。
-- 仅处理 enabled 且非 embedding 的模型（embedding 独占 CapEmbedding，不混 chat）。
-- 幂等守卫 NOT ('tool_use' = ANY(capabilities))：重复执行安全，且不覆盖
-- 管理员已手动去掉 tool_use 的模型（语义与 inferCapabilities 注释一致）。

UPDATE public.models
SET capabilities = array_append(capabilities, 'tool_use'), updated_at = now()
WHERE enabled
  AND NOT ('tool_use' = ANY(capabilities))
  AND NOT ('embedding' = ANY(capabilities));
