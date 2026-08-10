import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { scheduledTaskApi } from '../api/scheduledTask.api';
import type {
  CreateScheduledTaskInput,
  ScheduledTask,
  UpdateScheduledTaskInput,
} from '../model/scheduledTask';

import { SCHEDULED_TASK_DEFAULT_PAGE_SIZE } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

export function useScheduledTasks() {
  const [tasks, setTasks] = useState<ScheduledTask[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(SCHEDULED_TASK_DEFAULT_PAGE_SIZE);
  const [loading, setLoading] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const fetch = useCallback(async (nextPage = 1, nextPageSize = SCHEDULED_TASK_DEFAULT_PAGE_SIZE) => {
    setLoading(true);
    try {
      const data = await scheduledTaskApi.listScheduledTasks({ page: nextPage, pageSize: nextPageSize });
      setTasks(data.tasks);
      setTotal(data.total);
      setPage(data.page);
      setPageSize(data.pageSize);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载定时任务失败'), duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetch();
  }, [fetch]);

  const changePage = useCallback((nextPage: number, nextPageSize: number) => {
    void fetch(nextPage, nextPageSize);
  }, [fetch]);

  const createTask = useCallback(async (payload: CreateScheduledTaskInput) => {
    setCreateLoading(true);
    try {
      await scheduledTaskApi.createScheduledTask(payload);
      message.success({ content: '定时任务已创建', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '创建定时任务失败'), duration: 0 });
      throw err;
    } finally {
      setCreateLoading(false);
    }
  }, [fetch]);

  const updateTask = useCallback(async (id: string, payload: UpdateScheduledTaskInput) => {
    setCreateLoading(true);
    try {
      await scheduledTaskApi.updateScheduledTask(id, payload);
      message.success({ content: '定时任务已更新', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新定时任务失败'), duration: 0 });
      throw err;
    } finally {
      setCreateLoading(false);
    }
  }, [fetch]);

  const deleteTask = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await scheduledTaskApi.deleteScheduledTask(id);
      message.success({ content: '定时任务已删除', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '删除定时任务失败'), duration: 0 });
    } finally {
      setDeleteLoading(false);
    }
  }, [fetch]);

  const setEnabled = useCallback(async (id: string, enabled: boolean) => {
    try {
      await scheduledTaskApi.setScheduledTaskEnabled(id, enabled);
      setTasks((prev) => prev.map((t) => (t.id === id ? { ...t, enabled } : t)));
      message.success({ content: enabled ? '定时任务已启用' : '定时任务已停用', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新定时任务状态失败'), duration: 0 });
    }
  }, []);

  return {
    tasks,
    total,
    page,
    pageSize,
    loading,
    createLoading,
    deleteLoading,
    refresh: fetch,
    changePage,
    createTask,
    updateTask,
    deleteTask,
    setEnabled,
  };
}
