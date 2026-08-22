import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import {
  buildGroupedModels,
  type Agent,
  type AgentFormValues,
  type GroupedModelOption,
} from '../model/agent';

import { AGENT_DEFAULT_MAX_CONTEXT_TOKENS, AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
import { knowledgeApi } from '@/modules/knowledge';
import type { Workspace } from '@/modules/knowledge';
import { llmApi } from '@/modules/llm';
import { mcpApi } from '@/modules/mcp';
import type { MCPToolOption } from '@/modules/mcp';
import { skillApi } from '@/modules/skill';
import type { Skill } from '@/modules/skill';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

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
  const managementPath = '/agents';

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
        // 系统内置资源（isSystem skill / platform_managed workspace）仅系统助手
        // 可挂载。系统助手 disabled 表单仍显示全部（供展示）；普通 agent 选择列
        // 过滤。prefilled 不滤：普通 agent 已挂 platform 资源保存时由后端
        // A1/A2 拒 409，前端展示错误即可。
        if (skillsRes.status === 'fulfilled') {
          setSkills(a.isSystem ? skillsRes.value : skillsRes.value.filter((s) => !s.isSystem));
        }
        if (mcpRes.status === 'fulfilled') setMcpTools(mcpRes.value);
        if (workspacesRes.status === 'fulfilled') {
          setWorkspaces(a.isSystem ? workspacesRes.value : workspacesRes.value.filter((w) => w.management_mode !== 'platform_managed'));
        }
        if (modelsRes.status === 'fulfilled' && providersRes.status === 'fulfilled') {
          setGroupedModels(buildGroupedModels(modelsRes.value, providersRes.value));
        } else {
          const failed = [modelsRes, providersRes].find((r) => r.status === 'rejected');
          if (failed && failed.status === 'rejected') {
            message.error({ content: extractErrorMessage(failed.reason, '加载模型目录失败'), duration: 3 });
          }
        }
        form.setFieldsValue({
          name: a.name,
          description: a.description,
          systemPrompt: a.systemPrompt,
          llmModel: a.llmModel,
          maxIterations: a.maxIterations ?? AGENT_DEFAULT_MAX_ITERATIONS,
          maxContextTokens: a.maxContextTokens ?? AGENT_DEFAULT_MAX_CONTEXT_TOKENS,
          temperature: a.temperature,
          max_tokens: a.max_tokens,
          compaction_recent_groups: a.compaction_recent_groups,
          // compaction_prompt/temperature/model 是顶层 DTO 字段，GET 回显顶层。
          compaction_prompt: a.compaction_prompt,
          compaction_temperature: a.compaction_temperature,
          compaction_model: a.compaction_model,
          allowedSkills: a.allowedSkills || [],
          mcpToolIds: a.mcpToolIds || [],
          knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
          memoryScope: a.memoryScope || 'user',
        });
      } catch (err) {
        if (!cancelled) {
          message.error({ content: extractErrorMessage(err, '加载 Agent 信息失败'), duration: 3 });
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
        const {
          ...rest
        } = values;
        await agentApi.update(id, {
          ...rest,
          mcpToolIds: values.mcpToolIds || [],
          knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
        });
        message.success({ content: `Agent "${values.name}" 保存成功`, duration: 2 });
        navigate(agent?.isSystem ? '/agents' : '/agents');
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存失败'), duration: 3 });
        }
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
