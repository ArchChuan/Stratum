import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import type { AgentFormValues } from '../model/agent';

import { knowledgeApi } from '@/modules/knowledge';
import type { Workspace } from '@/modules/knowledge';
import { llmApi } from '@/modules/llm';
import { mcpApi } from '@/modules/mcp';
import type { MCPToolOption } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';
import type { Skill } from '@/modules/skill';
import { extractErrorMessage } from '@/shared/lib';

import type { GroupedModelOption } from '../components/AgentFormSections';

export const useCreateAgentPage = () => {
  const [form] = Form.useForm<AgentFormValues>();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpTools, setMcpTools] = useState<MCPToolOption[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [groupedModels, setGroupedModels] = useState<GroupedModelOption[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [skillsRes, mcpRes, workspacesRes, modelsRes, providersRes] = await Promise.allSettled([
        skillApi.list(),
        mcpApi.toolOptions(),
        knowledgeApi.list(),
        llmApi.listModels({ capability: 'chat' }),
        llmApi.listProviders(),
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
    })();
    return () => {
      cancelled = true;
    };
  }, [form]);

  const onFinish = useCallback(
    async (values: AgentFormValues) => {
      setLoading(true);
      try {
        await agentApi.create({
          ...values,
          mcpToolIds: values.mcpToolIds || [],
          knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
        });
        message.success(`Agent "${values.name}" 创建成功`);
        navigate('/agents/list');
      } catch (err) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) message.error(extractErrorMessage(err) || '创建失败');
      } finally {
        setLoading(false);
      }
    },
    [navigate],
  );

  return { form, loading, skills, mcpTools, workspaces, groupedModels, navigate, onFinish };
};
