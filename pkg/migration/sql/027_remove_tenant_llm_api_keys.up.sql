UPDATE public.tenants
SET settings = COALESCE(settings, '{}'::jsonb) - 'llm_api_keys'
WHERE COALESCE(settings, '{}'::jsonb) ? 'llm_api_keys';
