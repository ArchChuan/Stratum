import { message } from 'antd';
import { useEffect, useMemo, useState } from 'react';

import { agentApi } from '@/modules/agent/api/agent.api';
import { mcpApi } from '@/modules/mcp/api/mcp.api';
import { skillApi } from '@/modules/skill/api/skill.api';

type Option = { value: string; label: string };
interface RequestError { response?: { data?: { error?: string } } }

const errorText = (error: unknown) => (error as RequestError).response?.data?.error || '操作失败';

export const useWorkflowResources = () => {
  const [agents, setAgents] = useState<Option[]>([]);
  const [skills, setSkills] = useState<Option[]>([]);
  const [skillRevisions, setSkillRevisions] = useState<Option[]>([]);
  const [mcpServers, setMCPServers] = useState<Option[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      const results = await Promise.allSettled([agentApi.list(), skillApi.list(), mcpApi.list()]);
      if (cancelled) return;

      const [agentResult, skillResult, mcpResult] = results;
      if (agentResult.status === 'fulfilled') {
        setAgents(agentResult.value.map((agent) => ({ value: agent.id, label: agent.name })));
      } else {
        message.error({ content: errorText(agentResult.reason), duration: 0 });
      }
      if (skillResult.status === 'fulfilled') {
        const published = skillResult.value.filter((skill) => skill.activeRevisionId);
        setSkills(published.map((skill) => ({ value: skill.id, label: skill.name })));
        setSkillRevisions(published.map((skill) => ({
          value: skill.activeRevisionId as string,
          label: `${skill.name}（已发布）`,
        })));
      } else {
        message.error({ content: errorText(skillResult.reason), duration: 0 });
      }
      if (mcpResult.status === 'fulfilled') {
        setMCPServers(mcpResult.value.map((server) => ({ value: server.id, label: server.name })));
      } else {
        message.error({ content: errorText(mcpResult.reason), duration: 0 });
      }
    };
    void load();
    return () => { cancelled = true; };
  }, []);

  return useMemo(
    () => ({ agents, skills, skillRevisions, mcpServers }),
    [agents, mcpServers, skillRevisions, skills],
  );
};
