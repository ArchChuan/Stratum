import api from '@/services/client';

export const memoryUserApi = {
  clearMyMemories: async (): Promise<void> => {
    await api.delete('/api/memory/clear');
  },
};
