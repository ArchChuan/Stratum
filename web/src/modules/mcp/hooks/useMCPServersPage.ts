import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { mcpApi } from '../api/mcp.api';
import type { MCPServer } from '../model/mcp';

import { extractErrorMessage } from '@/shared/lib';

export const useMCPServersPage = () => {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(false);
  const [detailServer, setDetailServer] = useState<MCPServer | null>(null);

  const fetchServers = useCallback(async () => {
    setLoading(true);
    try {
      const list = await mcpApi.list();
      setServers(list);
      return { ok: true as const };
    } catch {
      return { ok: false as const };
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshServers = useCallback(async () => {
    const r = await fetchServers();
    if (!r.ok) message.error('获取 MCP 服务器列表失败');
  }, [fetchServers]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const r = await fetchServers();
      if (!cancelled && !r.ok) message.error('获取 MCP 服务器列表失败');
    })();
    return () => {
      cancelled = true;
    };
  }, [fetchServers]);

  const handleDisconnect = useCallback(
    async (id: string) => {
      try {
        await mcpApi.disconnect(id);
        message.success('已断开连接');
        refreshServers();
      } catch (err: unknown) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) {
          message.error(extractErrorMessage(err) || '断开失败');
        }
      }
    },
    [refreshServers],
  );

  const handleReconnect = useCallback(
    async (id: string) => {
      try {
        await mcpApi.reconnect(id);
        message.success('已重新连接');
        refreshServers();
      } catch (err: unknown) {
        message.error(extractErrorMessage(err) || '连接失败');
      }
    },
    [refreshServers],
  );

  const handleDelete = useCallback(
    async (id: string) => {
      try {
        await mcpApi.delete(id);
        message.success('已删除');
        refreshServers();
      } catch (err: unknown) {
        message.error(extractErrorMessage(err) || '删除失败');
      }
    },
    [refreshServers],
  );

  return {
    servers,
    loading,
    detailServer,
    setDetailServer,
    fetchServers: refreshServers,
    handleDisconnect,
    handleReconnect,
    handleDelete,
  };
};
