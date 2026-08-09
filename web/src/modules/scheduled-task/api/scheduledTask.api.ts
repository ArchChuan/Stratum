import {
  scheduledTaskPageSchema,
  scheduledTaskSchema,
  type CreateScheduledTaskInput,
  type ScheduledTask,
  type UpdateScheduledTaskInput,
} from '../model/scheduledTask';

import api from '@/services/client';

export const scheduledTaskApi = {
  listScheduledTasks: async ({ page, pageSize }: { page: number; pageSize: number }) => {
    const response = await api.get('/scheduled-tasks', { params: { page, page_size: pageSize } });
    return scheduledTaskPageSchema.parse(response.data);
  },

  getScheduledTask: async (id: string): Promise<ScheduledTask> => {
    const response = await api.get(`/scheduled-tasks/${id}`);
    return scheduledTaskSchema.parse(response.data);
  },

  createScheduledTask: async (payload: CreateScheduledTaskInput): Promise<ScheduledTask> => {
    const response = await api.post('/scheduled-tasks', payload);
    return scheduledTaskSchema.parse(response.data);
  },

  updateScheduledTask: async (id: string, payload: UpdateScheduledTaskInput): Promise<ScheduledTask> => {
    const response = await api.put(`/scheduled-tasks/${id}`, payload);
    return scheduledTaskSchema.parse(response.data);
  },

  deleteScheduledTask: async (id: string): Promise<void> => {
    await api.delete(`/scheduled-tasks/${id}`);
  },

  setScheduledTaskEnabled: async (id: string, enabled: boolean): Promise<void> => {
    await api.patch(`/scheduled-tasks/${id}/enabled`, { enabled });
  },
};
