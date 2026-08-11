import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { mcpApi } from '../api/mcp.api';
import type { MCPQuota, MCPServer } from '../model/mcp';

import { extractErrorMessage, isForbidden } from '@/shared/lib';

export const useMCPServersPage = () => {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [quota, setQuota] = useState<MCPQuota | null>(null);
  const [detailServer, setDetailServer] = useState<MCPServer | null>(null);

  const fetchServers = useCallback(async () => {
    setLoading(true);
    try {
      const [list, q] = await Promise.all([
        mcpApi.list(),
        mcpApi.quota().catch(() => null),
      ]);
      setServers(list);
      setQuota(q);
      return { ok: true as const };
    } catch {
      return { ok: false as const };
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshServers = useCallback(async () => {
    const r = await fetchServers();
    if (!r.ok) message.error({ content: '获取 MCP 服务器列表失败', duration: 0 });
  }, [fetchServers]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const r = await fetchServers();
      if (!cancelled && !r.ok) message.error({ content: '获取 MCP 服务器列表失败', duration: 0 });
    })();
    return () => {
      cancelled = true;
    };
  }, [fetchServers]);

  const handleDisconnect = useCallback(
    async (id: string) => {
      try {
        await mcpApi.disconnect(id);
        message.success({ content: '已断开连接', duration: 2 });
        refreshServers();
      } catch (err: unknown) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err) || '断开失败', duration: 0 });
        }
      }
    },
    [refreshServers],
  );

  const handleReconnect = useCallback(
    async (id: string) => {
      try {
        await mcpApi.reconnect(id);
        message.success({ content: '已重新连接', duration: 2 });
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
        message.success({ content: '已删除', duration: 2 });
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
    quota,
    detailServer,
    setDetailServer,
    fetchServers: refreshServers,
    handleDisconnect,
    handleReconnect,
    handleDelete,
  };
};
