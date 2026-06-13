ALTER TABLE agents      DROP CONSTRAINT IF EXISTS agents_name_unique;
ALTER TABLE skills      DROP CONSTRAINT IF EXISTS skills_name_unique;
ALTER TABLE mcp_configs DROP CONSTRAINT IF EXISTS mcp_configs_name_unique;
