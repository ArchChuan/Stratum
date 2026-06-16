import { useState, useEffect, useCallback } from 'react';
import { message, Form } from 'antd';
import { useNavigate } from 'react-router-dom';
import { createAgent, getAllSkills, getMCPServers, listWorkspaces } from '../services/api';
import { CHAT_MODEL_OPTIONS } from '../constants';

const DEFAULT_MODEL = CHAT_MODEL_OPTIONS[0].options[1].value; // qwen-plus

const useCreateAgentPage = () => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState([]);
  const [mcpServers, setMcpServers] = useState([]);
  const [workspaces, setWorkspaces] = useState([]);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [skillsRes, mcpRes, workspacesRes] = await Promise.allSettled([
        getAllSkills(),
        getMCPServers(),
        listWorkspaces(),
      ]);
      if (!cancelled) {
        if (skillsRes.status === 'fulfilled') setSkills(skillsRes.value.data.skills || []);
        if (mcpRes.status === 'fulfilled') setMcpServers(mcpRes.value.data.servers || []);
        if (workspacesRes.status === 'fulfilled') setWorkspaces(workspacesRes.value.data.workspaces || []);
        form.setFieldValue('llmModel', DEFAULT_MODEL);
      }
    })();
    return () => { cancelled = true; };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const onFinish = useCallback(async (values) => {
    setLoading(true);
    try {
      await createAgent({
        ...values,
        mcpServerIds: values.mcpServerIds || [],
        knowledgeWorkspaceIds: values.knowledgeWorkspaceIds || [],
      });
      message.success(`Agent "${values.name}" 创建成功`);
      navigate('/agents');
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '创建失败');
      }
    } finally {
      setLoading(false);
    }
  }, [navigate]);

  return {
    form, loading, skills, mcpServers, workspaces,
    navigate, onFinish,
  };
};

export default useCreateAgentPage;
