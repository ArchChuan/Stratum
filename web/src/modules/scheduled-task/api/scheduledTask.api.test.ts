import { beforeEach, describe, expect, it, vi } from 'vitest';

import { scheduledTaskApi } from './scheduledTask.api';

const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('@/services/client', () => ({ default: client }));

const taskPayload = {
  id: 'task-1',
  name: 'nightly',
  workflowId: 'wf-1',
  versionId: 'ver-1',
  inputTemplate: { task: 'summarize' },
  cronExpr: '0 9 * * *',
  enabled: true,
  nextFireAt: '2026-08-09T13:00:00Z',
  lastRunAt: '2026-08-09T12:00:00Z',
  lastRunStatus: 'ok',
  lastErrorMessage: '',
  createdBy: 'admin-1',
  createdAt: '2026-08-09T11:00:00Z',
  updatedAt: '2026-08-09T11:00:00Z',
};

describe('scheduled task api', () => {
  beforeEach(() => {
    client.get.mockReset();
    client.post.mockReset();
    client.put.mockReset();
    client.patch.mockReset();
    client.delete.mockReset();
  });

  it('lists tasks with page params through the shared client', async () => {
    client.get.mockResolvedValue({ data: { tasks: [taskPayload], total: 1, page: 1, pageSize: 20 } });
    const page = await scheduledTaskApi.listScheduledTasks({ page: 1, pageSize: 20 });
    expect(client.get).toHaveBeenCalledWith('/scheduled-tasks', { params: { page: 1, page_size: 20 } });
    expect(page.tasks).toHaveLength(1);
    expect(page.total).toBe(1);
  });

  it('normalizes a missing tasks array instead of failing parse', async () => {
    client.get.mockResolvedValue({ data: { tasks: undefined, total: 0, page: 1, pageSize: 20 } });
    const page = await scheduledTaskApi.listScheduledTasks({ page: 1, pageSize: 20 });
    expect(page.tasks).toEqual([]);
  });

  it('creates a task and parses the response', async () => {
    client.post.mockResolvedValue({ data: taskPayload });
    const task = await scheduledTaskApi.createScheduledTask({
      name: 'nightly',
      workflowId: 'wf-1',
      versionId: 'ver-1',
      inputTemplate: { task: 'summarize' },
      cronExpr: '0 9 * * *',
    });
    expect(client.post).toHaveBeenCalledWith('/scheduled-tasks', {
      name: 'nightly',
      workflowId: 'wf-1',
      versionId: 'ver-1',
      inputTemplate: { task: 'summarize' },
      cronExpr: '0 9 * * *',
    });
    expect(task.id).toBe('task-1');
    expect(task.inputTemplate).toEqual({ task: 'summarize' });
  });

  it('updates a task by id', async () => {
    client.put.mockResolvedValue({ data: taskPayload });
    await scheduledTaskApi.updateScheduledTask('task-1', { name: 'renamed', workflowId: 'wf-1',
      versionId: 'ver-1', inputTemplate: {}, cronExpr: '0 8 * * *' });
    expect(client.put).toHaveBeenCalledWith('/scheduled-tasks/task-1', {
      name: 'renamed', workflowId: 'wf-1', versionId: 'ver-1', inputTemplate: {}, cronExpr: '0 8 * * *',
    });
  });

  it('deletes a task by id', async () => {
    client.delete.mockResolvedValue({ data: {} });
    await scheduledTaskApi.deleteScheduledTask('task-1');
    expect(client.delete).toHaveBeenCalledWith('/scheduled-tasks/task-1');
  });

  it('toggles enabled without leaking other fields', async () => {
    client.patch.mockResolvedValue({ data: {} });
    await scheduledTaskApi.setScheduledTaskEnabled('task-1', false);
    expect(client.patch).toHaveBeenCalledWith('/scheduled-tasks/task-1/enabled', { enabled: false });
    expect(client.patch.mock.calls[0][1]).toEqual({ enabled: false });
  });

  it('rejects unexpected sensitive response fields', async () => {
    client.get.mockResolvedValue({ data: { ...taskPayload, rawInputTemplate: 'secret' } });
    await expect(scheduledTaskApi.getScheduledTask('task-1')).rejects.toThrow();
  });

  it('rejects a malformed task payload', async () => {
    client.get.mockResolvedValue({ data: { ...taskPayload, nextFireAt: undefined } });
    await expect(scheduledTaskApi.getScheduledTask('task-1')).rejects.toThrow();
  });
});
