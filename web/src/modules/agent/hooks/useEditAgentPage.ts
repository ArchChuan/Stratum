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
import { useAuth, useEditorCandidates, useTenantRole } from '@/modules/iam';
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
  const { user } = useAuth();
  const { isAdmin } = useTenantRole();
  const { candidates: editorCandidates, loading: editorCandidatesLoading } = useEditorCandidates();

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
        // 等化后 builtin skill / platform workspace 对普通 agent 开放挂载，
        // 编辑页选择列不再过滤系统内置资源。
        if (skillsRes.status === 'fulfilled') {
          setSkills(skillsRes.value);
        }
        if (mcpRes.status === 'fulfilled') setMcpTools(mcpRes.value);
        if (workspacesRes.status === 'fulfilled') {
          setWorkspaces(workspacesRes.value);
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
          // 压缩五值（提示词/温度/模型/最近轮数/冷却）为平台级参数，
          // 不在 agent 表单/存储。
          allowedSkills: a.allowedSkills || [],
          mcpToolIds: a.mcpToolIds || [],
          knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
          memoryScope: a.memoryScope || 'user',
          // 委托配置：delegateEnabled 缺失按 false（存量默认关闭，后端 NOT NULL 恒有值，
          // ?? false 仅兜底旧响应/缺字段）；深度/步数 0=unset 直接回显。
          delegateEnabled: a.delegateEnabled ?? false,
          delegateMaxDepth: a.delegateMaxDepth,
          delegateDefaultMaxSteps: a.delegateDefaultMaxSteps,
          // P2：可编辑人白名单回显；保存时经 setEditors 单独持久化。
          editors: a.editors as string[] | undefined,
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
        const { editors: nextEditors, ...rest } = values;
        await agentApi.update(id, {
          ...rest,
          mcpToolIds: values.mcpToolIds || [],
          knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
        });
        // P2：可编辑人白名单单独持久化（普通更新请求体不带 editors）。
        if (nextEditors) {
          await agentApi.setEditors(id, nextEditors);
        }
        message.success({ content: `Agent "${values.name}" 保存成功`, duration: 2 });
        navigate('/agents');
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存失败'), duration: 3 });
        }
      } finally {
        setLoading(false);
      }
    },
    [id, navigate],
  );

  // P1/P2：白名单成员可编辑——admin/owner 恒可编辑；普通成员仅当命中 agent.editors
  // 白名单才可编辑，否则编辑页进入只读「查看配置」+「申请编辑权限」。
  const editorIds = (agent?.editors as string[] | undefined) ?? [];
  const readOnly = !isAdmin && !editorIds.includes(user?.sub ?? '');

  return {
    id, agent, form, loading, pageLoading, skills, mcpTools, workspaces, groupedModels,
    navigate, managementPath, onFinish, readOnly,
    editorCandidates, editorCandidatesLoading,
  };
};
