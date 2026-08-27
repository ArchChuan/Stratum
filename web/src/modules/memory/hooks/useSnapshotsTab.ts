import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemorySnapshot } from '../model/memory';

interface RequestError { response?: { data?: { error?: string } } }

export const useSnapshotsTab = () => {
  const [snapshots, setSnapshots] = useState<MemorySnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [saveLoading, setSaveLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listSnapshots();
      if (seq !== requestSeqRef.current) return;
      setSnapshots(data.snapshots);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载快照失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const updateSnapshot = useCallback(async (agentId: string, data: { work_context: string[]; personal_context: string[]; top_of_mind: string[] }) => {
    setSaveLoading(true);
    try {
      await memoryUserApi.updateSnapshot(agentId, data);
      message.success({ content: '快照已更新', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '更新快照失败', duration: 3 });
    } finally {
      setSaveLoading(false);
    }
  }, [load]);

  const deleteSnapshot = useCallback(async (agentId: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteSnapshot(agentId);
      message.success({ content: '快照已清空', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清空快照失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { snapshots, loading, saveLoading, deleteLoading, updateSnapshot, deleteSnapshot, reload: load };
};
