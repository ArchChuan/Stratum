-- 039: separate observed model capability from operator policy, and add the
-- public audit store required by the public provider/model catalog.

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    operator_context_window INT;
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    operator_max_tokens INT;
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    default_output_tokens INT;
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    context_window_source TEXT NOT NULL DEFAULT 'legacy_unknown';
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_tokens_source TEXT NOT NULL DEFAULT 'legacy_unknown';
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    context_window_observed_at TIMESTAMPTZ;
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_tokens_observed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS public.platform_resource_change_audits (
    id                TEXT PRIMARY KEY,
    scope             TEXT NOT NULL DEFAULT 'platform',
    resource_kind     TEXT NOT NULL,
    resource_id       TEXT NOT NULL,
    operation         TEXT NOT NULL,
    actor_id          TEXT NOT NULL DEFAULT '',
    actor_tenant_id   TEXT,
    actor_type        TEXT NOT NULL DEFAULT 'user',
    source            TEXT NOT NULL DEFAULT 'api',
    proposal_id       TEXT NOT NULL DEFAULT '',
    before_projection JSONB NOT NULL DEFAULT '{}',
    after_projection  JSONB NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_platform_rca_resource
    ON public.platform_resource_change_audits(resource_kind, resource_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_platform_rca_actor
    ON public.platform_resource_change_audits(actor_id, created_at DESC);
