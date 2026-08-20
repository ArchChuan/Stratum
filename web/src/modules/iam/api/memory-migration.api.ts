import api from '@/services/client';
import type {
  MemoryMigrationCostResponse,
  MemoryMigrationResponse,
  StartMemoryMigrationRequest,
} from '@/services/gen/memory';


// 租户级记忆嵌入模型平滑迁移（P5）：确认制切换、后台渐进 re-embed。
// 端点带 /tenant 前缀，由租户上下文中间件解析 tenantID；变更类操作仅 admin 可用。
export const memoryMigrationApi = {
  getCurrent: async (): Promise<MemoryMigrationResponse | null> => {
    const res = await api.get<MemoryMigrationResponse | null>('/tenant/memory/migrations/current');
    return res.data;
  },

  getCost: async (): Promise<MemoryMigrationCostResponse> => {
    const res = await api.get<MemoryMigrationCostResponse>('/tenant/memory/migrations/cost');
    return res.data;
  },

  start: async (toModel: string): Promise<MemoryMigrationResponse> => {
    const body: StartMemoryMigrationRequest = { to_model: toModel };
    const res = await api.post<MemoryMigrationResponse>('/tenant/memory/migrations', body);
    return res.data;
  },

  cancel: async (id: number): Promise<void> => {
    await api.post(`/tenant/memory/migrations/${id}/cancel`);
  },

  retry: async (id: number): Promise<void> => {
    await api.post(`/tenant/memory/migrations/${id}/retry`);
  },
};
