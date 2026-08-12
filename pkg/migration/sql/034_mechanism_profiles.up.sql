-- 034: 机制基线 model_profiles（global 共享，public schema）
-- 机制面参数（prompt 四键/管线模型引用/召回参数）从硬编码与 env 收敛到存储，
-- 获得版本化与回退能力。语义见 docs/agent/mechanism-baseline.md。

CREATE TABLE IF NOT EXISTS model_profiles (
    id            TEXT PRIMARY KEY,
    family_key    TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    model_matcher JSONB NOT NULL,           -- {"family_prefixes": [...]}
    baseline      JSONB NOT NULL,           -- {"prompts": {...}, "models": {...}, "recall": {...}}
    fingerprint   TEXT NOT NULL DEFAULT '',
    version       INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL DEFAULT 'active',  -- active|draft
    created_by    TEXT NOT NULL DEFAULT 'seed',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_profiles_family
    ON model_profiles (family_key);

CREATE INDEX IF NOT EXISTS idx_model_profiles_status
    ON model_profiles (status, updated_at);
