import { message } from 'antd';
import { useEffect, useState } from 'react';

import { memoryAdminApi, type MemoryDiagnostics } from '../api/memory-admin.api';

import { MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS } from '@/constants';

export const useMemoryDiagnostics = (tenantId: string | null) => {
  const [diagnostics, setDiagnostics] = useState<MemoryDiagnostics | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchDiagnostics = async () => {
    if (!tenantId) return;

    setLoading(true);
    setError(null);

    try {
      const data = await memoryAdminApi.getDiagnostics(tenantId);
      setDiagnostics(data);
    } catch (err: unknown) {
      const errMsg =
        (err as { response?: { data?: { error?: string } } }).response?.data?.error || '加载失败';
      setError(errMsg);
      message.error(errMsg);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!tenantId) return;

    void fetchDiagnostics();
    const interval = setInterval(() => void fetchDiagnostics(), MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS);

    return () => clearInterval(interval);
  }, [tenantId]);

  return { diagnostics, loading, error, refetch: fetchDiagnostics };
};
