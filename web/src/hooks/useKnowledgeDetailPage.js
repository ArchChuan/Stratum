import { useState, useEffect, useCallback } from 'react';
import { message, Form } from 'antd';
import { useParams, useNavigate } from 'react-router-dom';
import { getWorkspaceStats, updateWorkspace, ingestDocument, queryKnowledge } from '../services/api';
import { useAuth } from './useAuth';

const useKnowledgeDetailPage = () => {
  const { name } = useParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const [stats, setStats] = useState(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [configForm] = Form.useForm();
  const [configLoading, setConfigLoading] = useState(false);
  const [uploadLoading, setUploadLoading] = useState(false);
  const [queryForm] = Form.useForm();
  const [queryLoading, setQueryLoading] = useState(false);
  const [queryResult, setQueryResult] = useState(null);

  const fetchStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const res = await getWorkspaceStats(name);
      setStats(res.data);
      configForm.setFieldsValue({
        chunk_size: res.data.config?.chunk_size,
        chunk_overlap: res.data.config?.chunk_overlap,
        query_mode: res.data.config?.query_mode,
        top_k: res.data.config?.top_k,
      });
    } catch (err) {
      message.error(err.response?.data?.error || '获取知识库详情失败');
    } finally {
      setStatsLoading(false);
    }
  }, [name, configForm]);

  useEffect(() => {
    fetchStats();
  }, [fetchStats]);

  const handleConfigSave = useCallback(async (values) => {
    setConfigLoading(true);
    try {
      await updateWorkspace(name, {
        config: {
          embedding_model: stats?.config?.embedding_model,
          chunk_size: values.chunk_size,
          chunk_overlap: values.chunk_overlap,
          query_mode: values.query_mode,
          top_k: values.top_k,
        },
      });
      message.success('配置已保存');
      fetchStats();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '保存失败');
      }
    } finally {
      setConfigLoading(false);
    }
  }, [name, stats, fetchStats]);

  const handleUpload = useCallback(async ({ file }) => {
    const formData = new FormData();
    formData.append('workspace', name);
    formData.append('file', file);
    setUploadLoading(true);
    try {
      const res = await ingestDocument(formData);
      message.success(`上传成功，共 ${res.data.total_chunks} 个分块`);
      fetchStats();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '上传失败');
      }
    } finally {
      setUploadLoading(false);
    }
    return false;
  }, [name, fetchStats]);

  const handleQuery = useCallback(async (values) => {
    setQueryLoading(true);
    setQueryResult(null);
    try {
      const res = await queryKnowledge({
        question: values.question,
        workspace: name,
        mode: values.mode || stats?.config?.query_mode || 'hybrid',
        topK: values.top_k || stats?.config?.top_k || 5,
      });
      setQueryResult(res.data);
    } catch (err) {
      message.error(err.response?.data?.error || '查询失败');
    } finally {
      setQueryLoading(false);
    }
  }, [name, stats]);

  return {
    name, navigate, isAdmin,
    stats, statsLoading,
    configForm, configLoading,
    uploadLoading, queryForm, queryLoading, queryResult,
    handleConfigSave, handleUpload, handleQuery,
  };
};

export default useKnowledgeDetailPage;
