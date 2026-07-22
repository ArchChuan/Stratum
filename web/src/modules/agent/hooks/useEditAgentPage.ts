import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import type { AgentFormValues } from '../model/agent';

import { knowledgeApi } from '@/modules/knowledge';
import type { Workspace } from '@/modules/knowledge';
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
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpTools, setMcpTools] = useState<MCPToolOption[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [skillsRes, agentRes, mcpRes, workspacesRes] = await Promise.allSettled([
          skillApi.list(),
          agentApi.get(id),
          mcpApi.toolOptions(),
          knowledgeApi.list(),
        ]);
        if (cancelled) return;
        if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value);
        if (mcpRes.status === 'fulfilled') setMcpTools(mcpRes.value);
        if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value);

        if (agentRes.status === 'fulfilled') {
          const a = agentRes.value;
          form.setFieldsValue({
            name: a.name,
            description: a.description,
            systemPrompt: a.systemPrompt,
            llmModel: a.llmModel,
            maxIterations: a.maxIterations ?? 25,
            maxContextTokens: a.maxContextTokens ?? 8000,
            allowedSkills: a.allowedSkills || [],
            mcpToolIds: a.mcpToolIds || [],
            knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
            memoryScope: a.memoryScope || 'user',
          });
        } else {
          message.error(extractErrorMessage(agentRes.reason, '加载 Agent 信息失败'));
          navigate('/agents');
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
        navigate('/agents');
      } catch (err) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) message.error(extractErrorMessage(err) || '保存失败');
      } finally {
        setLoading(false);
      }
    },
    [id, navigate],
  );

  return { id, form, loading, pageLoading, skills, mcpTools, workspaces, navigate, onFinish };
};
