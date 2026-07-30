import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import type { Agent, AgentFormValues, GroupedModelOption } from '../model/agent';

import { AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
import { knowledgeApi } from '@/modules/knowledge';
import type { Workspace } from '@/modules/knowledge';
import { llmApi } from '@/modules/llm';
import { mcpApi } from '@/modules/mcp';
import type { MCPToolOption } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';
import type { Skill } from '@/modules/skill';
import { extractErrorMessage } from '@/shared/lib';

export const useEditAgentPage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const [form] = Form.useForm<AgentFormValues>();
  const [loading, setLoading] = useState(false);
  const [pageLoading, setPageLoading] = useState(true);
  const [agent, setAgent] = useState<Agent>();
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpTools, setMcpTools] = useState<MCPToolOption[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [groupedModels, setGroupedModels] = useState<GroupedModelOption[]>([]);
  const navigate = useNavigate();
  const managementPath = agent?.isSystem ? '/agents' : '/agents/list';

  useEffect(() => {
    let cancelled = false;
    setPageLoading(true);
    setAgent(undefined);
    setSkills([]);
    setMcpTools([]);
    setWorkspaces([]);
    setGroupedModels([]);
    (async () => {
      try {
        const a = await agentApi.get(id);
        if (cancelled) return;
        setAgent(a);

        const [skillsRes, mcpRes, workspacesRes, modelsRes, providersRes] = await Promise.allSettled([
          skillApi.list(), mcpApi.toolOptions(), knowledgeApi.list(),
          llmApi.listModels({ capability: 'chat' }), llmApi.listProviders(),
        ]);
        if (cancelled) return;
        if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value);
        if (mcpRes.status === 'fulfilled') setMcpTools(mcpRes.value);
        if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value);
        if (modelsRes.status === 'fulfilled' && providersRes.status === 'fulfilled') {
          const providers = providersRes.value;
          const models = modelsRes.value;
          const providerMap = new Map(providers.map((p) => [p.id, p.name]));
          const grouped = new Map<string, { value: string; label: string }[]>();
          for (const m of models) {
            const pName = providerMap.get(m.providerId) || m.providerId;
            if (!grouped.has(pName)) grouped.set(pName, []);
            grouped.get(pName)!.push({ value: m.name, label: m.displayName || m.name });
          }
          setGroupedModels(
            Array.from(grouped.entries()).map(([provider, models]) => ({ provider, models })),
          );
        } else {
          const failed = [modelsRes, providersRes].find((r) => r.status === 'rejected');
          if (failed && failed.status === 'rejected') {
            message.error({ content: extractErrorMessage(failed.reason, '加载模型目录失败'), duration: 0 });
          }
        }
        form.setFieldsValue({
          name: a.name,
          description: a.description,
          systemPrompt: a.systemPrompt,
          llmModel: a.llmModel,
          maxIterations: a.maxIterations ?? AGENT_DEFAULT_MAX_ITERATIONS,
          maxContextTokens: a.maxContextTokens ?? 8000,
          allowedSkills: a.allowedSkills || [],
          mcpToolIds: a.mcpToolIds || [],
          knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
          memoryScope: a.memoryScope || 'user',
          checkpointEnabled: a.checkpointEnabled ?? false,
        });
      } catch (err) {
        if (!cancelled) {
          message.error({ content: extractErrorMessage(err, '加载 Agent 信息失败'), duration: 0 });
          navigate('/agents/list');
        }
      } finally {
        if (!cancelled) setPageLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, form, navigate]);

  const onFinish = useCallback(
    async (values: AgentFormValues) => {
      setLoading(true);
      try {
        await agentApi.update(id, {
          ...values,
          mcpToolIds: values.mcpToolIds || [],
          knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
        });
        message.success(`Agent "${values.name}" 保存成功`);
        navigate(agent?.isSystem ? '/agents' : '/agents/list');
      } catch (err) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) message.error(extractErrorMessage(err) || '保存失败');
      } finally {
        setLoading(false);
      }
    },
    [id, navigate, agent?.isSystem],
  );

  return {
    id, agent, form, loading, pageLoading, skills, mcpTools, workspaces, groupedModels,
    navigate, managementPath, onFinish,
  };
};
