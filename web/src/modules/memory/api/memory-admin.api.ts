import api from '@/services/client';

export interface MemoryDiagnostics {
  total_facts: number;
  total_entities: number;
  total_relationships: number;
  avg_frecency: number;
  frecency_distribution: number[];
  top_entities: Array<{
    name: string;
    type: string;
    mention_count: number;
  }>;
  pipeline_status: {
    queue_depth: number;
    last_processed_at?: string;
    processing_rate_per_min?: number;
  };
}

export const memoryAdminApi = {
  getDiagnostics: async (tenantId: string): Promise<MemoryDiagnostics> => {
    const res = await api.get(`/admin/memory/diagnostics`, {
      params: { tenant_id: tenantId },
    });
    return res.data;
  },
};
