DROP INDEX IF EXISTS public.idx_tenants_is_default;

ALTER TABLE public.tenants
  DROP COLUMN IF EXISTS is_default;
