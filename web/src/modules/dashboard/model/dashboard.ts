export interface DashboardCounts {
  skills: number;
  agents: number;
  mcpServers: number;
  executions: number;
  knowledge: number;
}

export interface DashboardExecution {
  id: string;
  agent_name: string;
  status: 'success' | 'error' | string;
  input_preview?: string;
  output_preview?: string;
  error_message?: string;
  total_tokens?: number;
  duration_ms?: number;
  created_at: string;
}
