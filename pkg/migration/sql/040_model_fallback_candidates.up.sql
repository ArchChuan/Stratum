-- 040: 平台级显式降级候选链（per-model fallback candidates）
-- 为每个模型记录显式配置的降级候选模型名（有序，最优先在前）。降级链解析时
-- 显式候选优先，不足再由注册表按隐式规则补齐到 constants.MaxModelFallbackCandidates。
-- 候选必须是存在、enabled 且支持 chat 的模型（写入时 application 层 fail-closed 校验，
-- 运行期解析仍逐项容错跳过失效候选）。仅对 chat 能力模型有意义；embedding 走
-- 默认嵌入解析链，不消费此列。

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    fallback_candidates TEXT[] NOT NULL DEFAULT '{}';
