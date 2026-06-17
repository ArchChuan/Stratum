CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 用户表
CREATE TABLE IF NOT EXISTS users (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  github_id       TEXT UNIQUE NOT NULL,
  github_login    TEXT NOT NULL,
  email           TEXT,
  display_name    TEXT,
  avatar_url      TEXT,
  global_role     TEXT NOT NULL DEFAULT 'user',
  last_login_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 租户表
CREATE TABLE IF NOT EXISTS tenants (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name            TEXT NOT NULL,
  slug            TEXT UNIQUE NOT NULL,
  github_org_id   TEXT,
  github_org_name TEXT,
  avatar_url      TEXT,
  plan            TEXT NOT NULL DEFAULT 'free',
  status          TEXT NOT NULL DEFAULT 'active',
  settings        JSONB NOT NULL DEFAULT '{}',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);

-- 租户成员
CREATE TABLE IF NOT EXISTS tenant_members (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL DEFAULT 'member',
  invited_by      UUID REFERENCES users(id),
  joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, user_id)
);

-- 邀请
CREATE TABLE IF NOT EXISTS invitations (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email           TEXT NOT NULL,
  role            TEXT NOT NULL DEFAULT 'member',
  token_hash      TEXT NOT NULL,
  invited_by      UUID NOT NULL REFERENCES users(id),
  expires_at      TIMESTAMPTZ NOT NULL,
  accepted_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Refresh Token
CREATE TABLE IF NOT EXISTS refresh_tokens (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id       UUID REFERENCES tenants(id) ON DELETE CASCADE,
  token_hash      TEXT NOT NULL,
  device_info     JSONB,
  issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  last_used_at    TIMESTAMPTZ,
  revoked_at      TIMESTAMPTZ
);

-- 租户 API Key
CREATE TABLE IF NOT EXISTS tenant_api_keys (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  display_name    TEXT NOT NULL,
  key_hash        TEXT NOT NULL,
  key_prefix      TEXT NOT NULL,
  scopes          TEXT[] NOT NULL DEFAULT '{}',
  expires_at      TIMESTAMPTZ,
  last_used_at    TIMESTAMPTZ,
  created_by      UUID REFERENCES users(id),
  revoked_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 审计日志
CREATE TABLE IF NOT EXISTS audit_logs (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID REFERENCES tenants(id) ON DELETE SET NULL,
  user_id         UUID,
  action          TEXT NOT NULL,
  resource_type   TEXT,
  resource_id     TEXT,
  old_value       JSONB,
  new_value       JSONB,
  ip_address      TEXT,
  user_agent      TEXT,
  trace_id        TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 模型供应商
CREATE TABLE IF NOT EXISTS model_providers (
  id                    TEXT PRIMARY KEY,
  name                  TEXT NOT NULL,
  base_url              TEXT,
  auth_type             TEXT NOT NULL DEFAULT 'bearer',
  supports_completion   BOOL NOT NULL DEFAULT true,
  supports_embedding    BOOL NOT NULL DEFAULT false,
  supports_streaming    BOOL NOT NULL DEFAULT false,
  supports_vision       BOOL NOT NULL DEFAULT false,
  status                TEXT NOT NULL DEFAULT 'active',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 模型目录
CREATE TABLE IF NOT EXISTS models (
  id                        TEXT PRIMARY KEY,
  provider_id               TEXT NOT NULL REFERENCES model_providers(id),
  display_name              TEXT NOT NULL,
  type                      TEXT NOT NULL,
  context_window            INT,
  max_output_tokens         INT,
  input_cost_per_1k         NUMERIC(10,6),
  output_cost_per_1k        NUMERIC(10,6),
  embedding_dimensions      INT,
  supports_streaming        BOOL NOT NULL DEFAULT true,
  supports_function_calling BOOL NOT NULL DEFAULT false,
  supports_vision           BOOL NOT NULL DEFAULT false,
  supports_json_mode        BOOL NOT NULL DEFAULT false,
  is_deprecated             BOOL NOT NULL DEFAULT false,
  deprecated_at             TIMESTAMPTZ,
  available_from            TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata                  JSONB NOT NULL DEFAULT '{}'
);

-- 内置供应商种子数据
INSERT INTO model_providers (id, name, base_url, auth_type, supports_completion, supports_embedding, supports_streaming)
VALUES
  ('openai',    'OpenAI',    'https://api.openai.com/v1',    'bearer',    true, true,  true),
  ('anthropic', 'Anthropic', 'https://api.anthropic.com/v1', 'x-api-key', true, false, true),
  ('ollama',    'Ollama',    'http://localhost:11434',        'none',      true, false, true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO models (id, provider_id, display_name, type, context_window, max_output_tokens, input_cost_per_1k, output_cost_per_1k, supports_function_calling)
VALUES
  ('gpt-4o',                 'openai',    'GPT-4o',              'completion', 128000, 4096,  0.005000, 0.015000, true),
  ('gpt-4o-mini',            'openai',    'GPT-4o Mini',         'completion', 128000, 16384, 0.000150, 0.000600, true),
  ('text-embedding-3-small', 'openai',    'Embedding 3 Small',   'embedding',  8191,   NULL,  0.000020, 0.000000, false),
  ('claude-opus-4-6',        'anthropic', 'Claude Opus 4.6',     'completion', 200000, 4096,  0.015000, 0.075000, true),
  ('claude-sonnet-4-6',      'anthropic', 'Claude Sonnet 4.6',   'completion', 200000, 8096,  0.003000, 0.015000, true),
  ('claude-haiku-4-5',       'anthropic', 'Claude Haiku 4.5',    'completion', 200000, 4096,  0.000800, 0.004000, true)
ON CONFLICT (id) DO NOTHING;

-- 索引
CREATE INDEX IF NOT EXISTS idx_tenant_members_user    ON tenant_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant  ON tenant_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user    ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant      ON audit_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created     ON audit_logs(created_at DESC);
