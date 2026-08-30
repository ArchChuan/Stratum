// 人工评审池条目类型，字段对齐后端 domain.ReviewItem JSON（Task 10 契约）。
export interface ReviewItem {
  id: string;
  source_type: string;
  source_id: string;
  run_id?: string;
  trace_id?: string;
  resource_kind: string;
  resource_id: string;
  trigger_reason: string;
  snapshot: unknown;
  status: string;
  human_verdict?: string;
  reviewer?: string;
  review_reason?: string;
  created_at: string;
  reviewed_at?: string | null;
}

// POST /evaluations/review/:id/decision 请求体（proto ReviewItemDecisionRequest）。
export interface ReviewItemDecisionRequest {
  verdict: string;
  reason: string;
}
