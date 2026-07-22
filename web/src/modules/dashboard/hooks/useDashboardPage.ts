import { message } from 'antd';
import { useEffect, useState } from 'react';

import type { DashboardCounts } from '../model/dashboard';

import { agentApi } from '@/modules/agent';
import { knowledgeApi } from '@/modules/knowledge';
import { mcpApi } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';

const initialCounts: DashboardCounts = {
  skills: 0,
  agents: 0,
  mcpServers: 0,
  knowledge: 0,
};

export const useDashboardPage = () => {
  const [counts, setCounts] = useState<DashboardCounts>(initialCounts);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const [skillsRes, agentsRes, mcpRes, knowledgeRes] = await Promise.allSettled([
          skillApi.list(),
          agentApi.list(),
          mcpApi.list(),
          knowledgeApi.list(),
        ]);
        if (cancelled) return;

        const skills = skillsRes.status === 'fulfilled' ? skillsRes.value : [];
        const agents = agentsRes.status === 'fulfilled' ? agentsRes.value : [];
        const mcpServers = mcpRes.status === 'fulfilled' ? mcpRes.value : [];
        const workspaces = knowledgeRes.status === 'fulfilled' ? knowledgeRes.value : [];

        setCounts({
          skills: skills.length,
          agents: agents.length,
          mcpServers: mcpServers.length,
          knowledge: workspaces.length,
        });
      } catch {
        if (!cancelled) message.error('加载仪表盘数据失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return { counts, loading };
};

export default useDashboardPage;
