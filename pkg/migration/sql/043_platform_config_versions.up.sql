-- 043: 平台配置版本化。
--
-- 目标：平台配置免部署变更、可回滚、可归因。数据模型 = 分组
-- (platform_config_groups) + 不可变快照版本 (platform_config_versions)
-- + label 指针 (platform_config_labels)。只操作 public schema
-- (multi-tenant 规则，tenant-only DDL 的唯一基线是 tenant_schema.sql)。
--
-- 存量 backfill：platform_settings 是逐 key 行模型 (key PK, value JSONB)。
-- 按分组聚合，只投影 registry 已知的 40 个 platform-scope key（白名单）；
-- 未注册/已移除 key（如已下线 long_term_top_k、废弃 prompt.*）丢弃，
-- 防止把敏感残留带进快照。每分组生成 version_seq=1 的 published 版本
-- + production/latest label，created_by='system'。幂等：全 IF NOT EXISTS /
-- ON CONFLICT DO NOTHING，防 force 后重跑重复 version-1。

-- ① 分组表
CREATE TABLE IF NOT EXISTS platform_config_groups (
    group_key  TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ② 不可变版本表。draft 是唯一可编辑状态；published 快照只读；
-- archived 由保留上限自动修剪。base_version_id 记录 Publish 时
-- production 所指版本（回滚后 diff 链指向真实生效的父版本）。
CREATE TABLE IF NOT EXISTS platform_config_versions (
    id              BIGSERIAL PRIMARY KEY,
    group_key       TEXT NOT NULL REFERENCES platform_config_groups(group_key),
    version_seq     INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft',
    snapshot        JSONB NOT NULL,
    base_version_id BIGINT REFERENCES platform_config_versions(id),
    message         TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (group_key, version_seq)
);

-- ③ label 指针表。production 是生效版本；latest 与 production 同步维护。
CREATE TABLE IF NOT EXISTS platform_config_labels (
    group_key  TEXT NOT NULL,
    label      TEXT NOT NULL,
    version_id BIGINT NOT NULL REFERENCES platform_config_versions(id),
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_key, label)
);

-- ④ 分组 seed（幂等）。与 internal/parameters/domain/registry.go 的
-- GroupKey 声明一致（registry 不变量测试守护）。
INSERT INTO platform_config_groups (group_key, name) VALUES
    ('agent',      'Agent'),
    ('memory',     'Memory'),
    ('evaluation', 'Evaluation'),
    ('trace',      'Trace')
ON CONFLICT (group_key) DO NOTHING;

-- ⑤ backfill：投影 registry 已知 platform-scope key，按分组聚合快照。
-- 白名单 VALUES 即 registry 平台键全集（40 个），硬编码与代码一致；
-- 前缀 CASE 只作用于白名单内 key，组归属唯一。
-- 空组也生成 '{}' 快照版本：统一 label 语义，运行时 label 恒存在，
-- 空快照 = 全部 unset = 回退 registry default（与无平台行一致）。
WITH grouped AS (
    SELECT
        CASE
            WHEN ps.key LIKE 'agent.%'      THEN 'agent'
            WHEN ps.key LIKE 'memory.%'     THEN 'memory'
            WHEN ps.key LIKE 'evaluation.%' THEN 'evaluation'
            ELSE 'trace'  -- 白名单内唯一非前缀键
        END AS group_key,
        ps.key,
        ps.value
    FROM platform_settings ps
    JOIN (VALUES
        -- agent (11)
        ('agent.compaction_prompt'),
        ('agent.compaction_temperature'),
        ('agent.compaction_model'),
        ('agent.compaction_recent_groups'),
        ('agent.compaction_cooldown_sec'),
        ('agent.system_prompt'),
        ('agent.factcheck.enabled'),
        ('agent.factcheck.judge.model'),
        ('agent.factcheck.judge.prompt'),
        ('agent.factcheck.top_k'),
        ('agent.factcheck.max_claims'),
        -- evaluation (6)
        ('evaluation.optimizer.model'),
        ('evaluation.optimizer.temperature'),
        ('evaluation.optimizer.max_tokens'),
        ('evaluation.judge.model'),
        ('evaluation.judge.temperature'),
        ('evaluation.judge.enabled'),
        -- trace (1)
        ('trace.capture_parameters'),
        -- memory (22)
        ('memory.recall_top_k'),
        ('memory.fact_injection_top_n'),
        ('memory.history_injection_top_n'),
        ('memory.max_facts_per_extraction'),
        ('memory.extraction_prompt'),
        ('memory.extraction_model'),
        ('memory.reflection_prompt'),
        ('memory.reflection_model'),
        ('memory.enrich_prompt'),
        ('memory.enrich_temperature'),
        ('memory.enrich_model'),
        ('memory.summary_prompt'),
        ('memory.summary_temperature'),
        ('memory.summary_model'),
        ('memory.embedding_model'),
        ('memory.history_summary_prompt'),
        ('memory.history_summary_temperature'),
        ('memory.history_summary_model'),
        ('memory.supersede_prompt'),
        ('memory.supersede_temperature'),
        ('memory.supersede_model'),
        ('memory.summary_token_threshold')
    ) AS known_keys(key) ON known_keys.key = ps.key
)
INSERT INTO platform_config_versions
    (group_key, version_seq, status, snapshot, message, created_by)
SELECT
    g.group_key,
    1,
    'published',
    COALESCE(gp.snapshot, '{}'::jsonb),
    'backfill from platform_settings',
    'system'
FROM platform_config_groups g
LEFT JOIN (
    SELECT group_key, jsonb_object_agg(key, value) AS snapshot
    FROM grouped
    GROUP BY group_key
) gp USING (group_key)
ON CONFLICT (group_key, version_seq) DO NOTHING;

-- ⑥ backfill label：production + latest 指向各分组 version-1。
INSERT INTO platform_config_labels (group_key, label, version_id, updated_by)
SELECT v.group_key, l.label, v.id, 'system'
FROM platform_config_versions v
CROSS JOIN (VALUES ('production'), ('latest')) AS l(label)
WHERE v.version_seq = 1
ON CONFLICT (group_key, label) DO NOTHING;
