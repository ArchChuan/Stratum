import { message } from 'antd';
import { useEffect, useState } from 'react';

import { dashboardApi } from '../api/dashboard.api';
import type { DashboardCounts } from '../model/dashboard';

const initialCounts: DashboardCounts = {
  agents: 0,
  skills: 0,
  knowledge_workspaces: 0,
  mcp_servers: 0,
  model_providers: 0,
  tenant_members: 0,
  workflows: 0,
  agent_user_messages_7d: 0,
};

export const useDashboardPage = () => {
  const [counts, setCounts] = useState<DashboardCounts>(initialCounts);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const data = await dashboardApi.overview();
        if (!cancelled) setCounts(data);
      } catch {
        if (!cancelled) message.error({ content: '加载概览数据失败', duration: 0 });
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  return { counts, loading };
};

export default useDashboardPage;
