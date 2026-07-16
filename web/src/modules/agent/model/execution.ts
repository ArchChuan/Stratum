export interface ExecutionRow {
  id: string;
  trace_id?: string;
  agent_name?: string;
  status?: string;
  input_preview?: string;
  output_preview?: string;
  error_message?: string;
  total_tokens?: number;
  duration_ms?: number;
  created_at?: string;
}
