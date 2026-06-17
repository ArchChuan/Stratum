ALTER TABLE public.tenants
  ADD COLUMN IF NOT EXISTS is_default BOOL NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_is_default
  ON public.tenants (is_default)
  WHERE is_default = true;
