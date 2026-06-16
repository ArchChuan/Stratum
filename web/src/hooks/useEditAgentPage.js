import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { getAgentById, updateAgent, getAllSkills, getMCPServers, listWorkspaces } from '../services/api';
import { useNavigate, useParams } from 'react-router-dom';
import { Form } from 'antd';

const useEditAgentPage = () => {
  const { id } = useParams();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [pageLoading, setPageLoading] = useState(true);
  const [skills, setSkills] = useState([]);
  const [mcpServers, setMcpServers] = useState([]);
  const [workspaces, setWorkspaces] = useState([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [skillsRes, agentRes, mcpRes, workspacesRes] = await Promise.allSettled([
          getAllSkills(),
          getAgentById(id),
          getMCPServers(),
          listWorkspaces(),
        ]);
        if (cancelled) return;

        if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value.data.skills || []);
        if (mcpRes.status === 'fulfilled') setMcpServers(mcpRes.value.data.servers || []);
        if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value.data.workspaces || []);

        if (agentRes.status === 'fulfilled') {
          const a = agentRes.value.data;
          form.setFieldsValue({
            name: a.name,
            description: a.description,
            type: a.type || 'react',
            persona: a.persona,
            systemPrompt: a.systemPrompt,
            llmModel: a.llmModel,
            maxIterations: a.maxIterations,
            maxContextTokens: a.maxContextTokens || 8000,
            allowedSkills: a.allowedSkills || [],
            mcpServerIds: a.mcpServerIds || [],
            knowledgeWorkspaceIds: a.knowledgeWorkspaceIds || [],
          });
        } else {
          message.error('加载 Agent 信息失败');
          navigate('/agents');
        }
      } finally {
        if (!cancelled) setPageLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  const onFinish = useCallback(async (values) => {
    setLoading(true);
    try {
      await updateAgent(id, {
        ...values,
        mcpServerIds: values.mcpServerIds || [],
        knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
      });
      message.success(`Agent "${values.name}" 保存成功`);
      navigate('/agents');
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '保存失败');
      }
    } finally {
      setLoading(false);
    }
  }, [id, navigate]);

  return {
    id, form, loading, pageLoading,
    skills, mcpServers, workspaces,
    navigate, onFinish,
  };
};

export default useEditAgentPage;
