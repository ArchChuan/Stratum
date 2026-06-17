import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import type { AgentFormValues } from '../model/agent';

import { CHAT_MODEL_OPTIONS } from '@/constants';
import { knowledgeApi } from '@/modules/knowledge';
import type { Workspace } from '@/modules/knowledge';
import { mcpApi } from '@/modules/mcp';
import type { MCPServer } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';
import type { Skill } from '@/modules/skill';
import { extractErrorMessage } from '@/shared/lib';

const DEFAULT_MODEL = CHAT_MODEL_OPTIONS[0].options[1].value;

export const useCreateAgentPage = () => {
  const [form] = Form.useForm<AgentFormValues>();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [skillsRes, mcpRes, workspacesRes] = await Promise.allSettled([
        skillApi.list(),
        mcpApi.list(),
        knowledgeApi.list(),
      ]);
      if (cancelled) return;
      if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value);
      if (mcpRes.status === 'fulfilled') setMcpServers(mcpRes.value);
      if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value);
      form.setFieldValue('llmModel', DEFAULT_MODEL);
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
          mcpServerIds: values.mcpServerIds || [],
          knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
        });
        message.success(`Agent "${values.name}" 创建成功`);
        navigate('/agents');
      } catch (err) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) message.error(extractErrorMessage(err) || '创建失败');
      } finally {
        setLoading(false);
      }
    },
    [navigate],
  );

  return { form, loading, skills, mcpServers, workspaces, navigate, onFinish };
};
