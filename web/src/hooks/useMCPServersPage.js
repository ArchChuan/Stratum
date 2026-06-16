import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { getMCPServers, disconnectMCPServer } from '../services/api';

const useMCPServersPage = () => {
  const [servers, setServers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [detailServer, setDetailServer] = useState(null);

  const fetchServers = useCallback(async () => {
    setLoading(true);
    try {
      const res = await getMCPServers();
      setServers(res.data?.servers || []);
    } catch {
      message.error('获取 MCP 服务器列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetchServers(); }, [fetchServers]);

  const handleDisconnect = useCallback(async (id) => {
    try {
      await disconnectMCPServer(id);
      message.success('已断开连接');
      fetchServers();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '断开失败');
      }
    }
  }, [fetchServers]);

  return {
    servers, loading,
    detailServer, setDetailServer,
    fetchServers, handleDisconnect,
  };
};

export default useMCPServersPage;

