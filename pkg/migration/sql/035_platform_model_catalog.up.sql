-- 035: 平台全局模型目录（public schema，去 tenant_id）
-- providers/models 从 tenant schema 提升为平台全局资源，账户统一走平台模型/凭据。
-- 语义见 docs/superpowers/specs/2026-08-13-model-management-refactor-design.md。

CREATE TABLE IF NOT EXISTS providers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL,
    base_url      TEXT NOT NULL DEFAULT '',
    api_key       TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS models (
    id                TEXT PRIMARY KEY,
    provider_id       TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    capabilities      TEXT[] NOT NULL DEFAULT '{}',
    context_window    INT NOT NULL DEFAULT 0,
    max_tokens        INT NOT NULL DEFAULT 0,
    input_price       DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price      DOUBLE PRECISION NOT NULL DEFAULT 0,
    recommended       BOOLEAN NOT NULL DEFAULT false,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    provider_managed  BOOLEAN NOT NULL DEFAULT false,
    default_embedding BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(provider_id, name)
);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);

-- 全局唯一默认嵌入模型标记：partial unique index 去 tenant 谓词（原 idx_models_default_embedding 带 tenant_id）。
-- 常量表达式索引 (true)：所有满足 WHERE 的行取同一常量，唯一约束强制全表最多一个默认标记。
CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
    ON models ((true))
    WHERE default_embedding AND 'embedding' = ANY(capabilities);
