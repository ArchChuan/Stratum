import { message } from 'antd';
import { useEffect, useMemo, useState } from 'react';

import { agentApi } from '@/modules/agent/api/agent.api';
import { mcpApi } from '@/modules/mcp/api/mcp.api';
import { skillApi } from '@/modules/skill/api/skill.api';
import { extractErrorMessage } from '@/shared/lib';

type Option = { value: string; label: string };

export const useWorkflowResources = () => {
  const [agents, setAgents] = useState<Option[]>([]);
  const [skills, setSkills] = useState<Option[]>([]);
  const [skillRevisions, setSkillRevisions] = useState<Option[]>([]);
  const [mcpServers, setMCPServers] = useState<Option[]>([]);
  // agent id → allowedSkills 映射，供 inspector 的 skill-agent 双向联动过滤（空 = 无技能）。
  const [agentAllowedSkills, setAgentAllowedSkills] = useState<Record<string, string[]>>({});

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      const results = await Promise.allSettled([agentApi.list(), skillApi.list(), mcpApi.list()]);
      if (cancelled) return;

      const [agentResult, skillResult, mcpResult] = results;
      if (agentResult.status === 'fulfilled') {
        setAgents(agentResult.value.map((agent) => ({ value: agent.id, label: agent.name })));
        setAgentAllowedSkills(Object.fromEntries(agentResult.value.map((agent) => [agent.id, agent.allowedSkills])));
      } else {
        message.error({ content: extractErrorMessage(agentResult.reason), duration: 3 });
      }
      if (skillResult.status === 'fulfilled') {
        // 等化后 builtin skill 对普通 workflow 开放挂载：只保留已发布修订。
        const published = skillResult.value.filter((skill) => skill.activeRevisionId);
        setSkills(published.map((skill) => ({ value: skill.id, label: skill.name })));
        setSkillRevisions(published.map((skill) => ({
          value: skill.activeRevisionId as string,
          label: `${skill.name}（已发布）`,
        })));
      } else {
        message.error({ content: extractErrorMessage(skillResult.reason), duration: 3 });
      }
      if (mcpResult.status === 'fulfilled') {
        setMCPServers(mcpResult.value.map((server) => ({ value: server.id, label: server.name })));
      } else {
        message.error({ content: extractErrorMessage(mcpResult.reason), duration: 3 });
      }
    };
    void load();
    return () => { cancelled = true; };
  }, []);

  return useMemo(
    () => ({ agents, skills, skillRevisions, mcpServers, agentAllowedSkills }),
    [agentAllowedSkills, agents, mcpServers, skillRevisions, skills],
  );
};
