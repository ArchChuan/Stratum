CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE OR REPLACE FUNCTION public.gen_uuid_v7() RETURNS uuid AS $$
DECLARE
    v_time BIGINT := (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT;
    v_rand BYTEA  := gen_random_bytes(10);
BEGIN
    RETURN encode(
        set_byte(
            set_byte(
                substr(int8send(v_time), 3, 6) || v_rand,
                6, (get_byte(v_rand, 0) & 15) | 112
            ),
            8, (get_byte(v_rand, 2) & 63) | 128
        ),
        'hex'
    )::uuid;
END;
$$ LANGUAGE plpgsql VOLATILE;

CREATE TABLE IF NOT EXISTS public.users (
  id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  github_id     TEXT        UNIQUE NOT NULL,
  github_login  TEXT        NOT NULL,
  email         TEXT,
  display_name  TEXT,
  avatar_url    TEXT,
  global_role   TEXT        NOT NULL DEFAULT 'user',
  last_login_at TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tenants (
  id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  name            TEXT        NOT NULL,
  slug            TEXT        UNIQUE NOT NULL,
  github_org_id   TEXT,
  github_org_name TEXT,
  avatar_url      TEXT,
  plan            TEXT        NOT NULL DEFAULT 'free',
  status          TEXT        NOT NULL DEFAULT 'active',
  settings        JSONB       NOT NULL DEFAULT '{}',
  is_default      BOOL        NOT NULL DEFAULT false,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS public.tenant_members (
  id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id  UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  user_id    UUID        NOT NULL REFERENCES public.users(id)   ON DELETE CASCADE,
  role       TEXT        NOT NULL DEFAULT 'member',
  invited_by UUID        REFERENCES public.users(id),
  joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, user_id)
);

CREATE TABLE IF NOT EXISTS public.refresh_tokens (
  id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id      UUID        NOT NULL REFERENCES public.users(id)   ON DELETE CASCADE,
  tenant_id    UUID        REFERENCES public.tenants(id)          ON DELETE CASCADE,
  token_hash   TEXT        NOT NULL,
  device_info  JSONB,
  issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  last_used_at TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ
);

-- Backfill columns for databases that predate consolidated DDL
ALTER TABLE public.tenants ADD COLUMN IF NOT EXISTS is_default BOOL NOT NULL DEFAULT false;

-- Indexes
CREATE INDEX        IF NOT EXISTS idx_tenant_members_user    ON public.tenant_members(user_id);
CREATE INDEX        IF NOT EXISTS idx_tenant_members_tenant  ON public.tenant_members(tenant_id);
CREATE INDEX        IF NOT EXISTS idx_refresh_tokens_user    ON public.refresh_tokens(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_is_default     ON public.tenants(is_default) WHERE is_default = true;

-- Drop deprecated tables
DROP TABLE IF EXISTS public.agent_executions;
DROP TABLE IF EXISTS public.models;
DROP TABLE IF EXISTS public.model_providers;
DROP TABLE IF EXISTS public.audit_logs;
DROP TABLE IF EXISTS public.tenant_api_keys;
DROP TABLE IF EXISTS public.invitations;
