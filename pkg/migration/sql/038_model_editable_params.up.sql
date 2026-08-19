-- 模型管理可编辑参数：采样默认值、采样上限、provider 级追加请求头与默认采样。
-- public schema 权威数据；运行时由 Gateway.enforceModelPolicy 消费。

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    sampling_params  JSONB NOT NULL DEFAULT '{}';
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_temperature  DOUBLE PRECISION;

ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    extra_headers    JSONB NOT NULL DEFAULT '{}';
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    default_sampling JSONB NOT NULL DEFAULT '{}';
