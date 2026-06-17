CREATE TABLE IF NOT EXISTS public.agent_executions (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID,
    agent_id       UUID        NOT NULL,
    user_id        UUID        NOT NULL,
    agent_name     TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL CHECK (status IN ('success', 'error')),
    input_preview  TEXT        NOT NULL DEFAULT '',
    output_preview TEXT        NOT NULL DEFAULT '',
    error_message  TEXT        NOT NULL DEFAULT '',
    total_tokens   INT         NOT NULL DEFAULT 0,
    duration_ms    INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agent_executions_tenant_created
    ON public.agent_executions (tenant_id, created_at DESC);
