DROP TABLE IF EXISTS public.platform_resource_change_audits;

ALTER TABLE public.models DROP COLUMN IF EXISTS max_tokens_observed_at;
ALTER TABLE public.models DROP COLUMN IF EXISTS context_window_observed_at;
ALTER TABLE public.models DROP COLUMN IF EXISTS max_tokens_source;
ALTER TABLE public.models DROP COLUMN IF EXISTS context_window_source;
ALTER TABLE public.models DROP COLUMN IF EXISTS default_output_tokens;
ALTER TABLE public.models DROP COLUMN IF EXISTS operator_max_tokens;
ALTER TABLE public.models DROP COLUMN IF EXISTS operator_context_window;
