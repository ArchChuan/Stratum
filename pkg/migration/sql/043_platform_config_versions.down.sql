-- 043 down: 删除版本化三表，保留 platform_settings（引用清零后单独清理，
-- 残留状态显式跟踪）。版本/标签是平台配置快照，非租户数据，直接 DROP。
-- 外键依赖顺序：labels → versions → groups。
DROP TABLE IF EXISTS platform_config_labels;
DROP TABLE IF EXISTS platform_config_versions;
DROP TABLE IF EXISTS platform_config_groups;
