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

export const useCreateAgentPage = () => {
  const [form] = Form.useForm<AgentFormValues>();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpTools, setMcpTools] = useState<MCPToolOption[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [chatModels, setChatModels] = useState<string[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [skillsRes, mcpRes, workspacesRes, modelsRes] = await Promise.allSettled([
        skillApi.list(),
        mcpApi.toolOptions(),
        knowledgeApi.list(),
        llmApi.getCatalogue(),
      ]);
      if (cancelled) return;
      if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value);
      if (mcpRes.status === 'fulfilled') setMcpTools(mcpRes.value);
      if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value);
      if (modelsRes.status === 'fulfilled') {
        setChatModels(modelsRes.value.chatModels);
      } else {
        message.error({ content: extractErrorMessage(modelsRes.reason, '加载模型目录失败'), duration: 0 });
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

  return { form, loading, skills, mcpTools, workspaces, chatModels, navigate, onFinish };
};
