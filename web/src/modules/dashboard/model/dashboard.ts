export interface DashboardCounts {
  agents: number;
  skills: number;
  knowledge_workspaces: number;
  mcp_servers: number;
  model_providers: number;
  tenant_members: number;
  workflows: number;
  agent_user_messages_7d: number;
}

export interface DashboardExecution {
  id: string;
  trace_id: string;
  agent_id: string;
  agent_name: string;
  status: 'success' | 'error' | string;
  input_preview?: string;
  output_preview?: string;
  error_message?: string;
  total_tokens?: number;
  duration_ms?: number;
  created_at: string;
}

export interface DashboardExecutionsPage {
  executions: DashboardExecution[];
  total: number;
}
