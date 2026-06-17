import { message } from 'antd';
import { useEffect, useState } from 'react';

import type { DashboardCounts, DashboardExecution } from '../model/dashboard';

import { agentApi } from '@/modules/agent';
import { knowledgeApi } from '@/modules/knowledge';
import { mcpApi } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';

const RECENT_EXEC_LIMIT = 8;

const initialCounts: DashboardCounts = {
  skills: 0,
  agents: 0,
  mcpServers: 0,
  executions: 0,
  knowledge: 0,
};

interface ExecutionsResponse {
  executions?: DashboardExecution[];
  total?: number;
}

export const useDashboardPage = () => {
  const [counts, setCounts] = useState<DashboardCounts>(initialCounts);
  const [recentExecs, setRecentExecs] = useState<DashboardExecution[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const [skillsRes, agentsRes, execsRes, mcpRes, knowledgeRes] = await Promise.allSettled([
          skillApi.list(),
          agentApi.list(),
          agentApi.executions(1, RECENT_EXEC_LIMIT),
          mcpApi.list(),
          knowledgeApi.list(),
        ]);
        if (cancelled) return;

        const skills = skillsRes.status === 'fulfilled' ? skillsRes.value : [];
        const agents = agentsRes.status === 'fulfilled' ? agentsRes.value : [];
        const mcpServers = mcpRes.status === 'fulfilled' ? mcpRes.value : [];
        const workspaces = knowledgeRes.status === 'fulfilled' ? knowledgeRes.value : [];

        let execs: DashboardExecution[] = [];
        let execsTotal = 0;
        if (execsRes.status === 'fulfilled') {
          const data = (execsRes.value.data ?? {}) as ExecutionsResponse;
          execs = data.executions ?? [];
          execsTotal = data.total ?? execs.length;
        }

        setCounts({
          skills: skills.length,
          agents: agents.length,
          mcpServers: mcpServers.length,
          executions: execsTotal,
          knowledge: workspaces.length,
        });
        setRecentExecs(execs.slice(0, RECENT_EXEC_LIMIT));
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

  return { counts, recentExecs, loading };
};

export default useDashboardPage;
