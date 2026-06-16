import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { Form } from 'antd';
import { useNavigate } from 'react-router-dom';
import { listWorkspaces, createWorkspace, deleteWorkspace } from '../services/api';
import { useAuth } from './useAuth';

const useKnowledgePage = () => {
  const [workspaces, setWorkspaces] = useState([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [searchText, setSearchText] = useState('');
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const res = await listWorkspaces();
        if (!cancelled) setWorkspaces(res.data.workspaces || []);
      } catch (err) {
        if (!cancelled) message.error(err.response?.data?.error || '获取知识库列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const handleCreate = useCallback(async (values) => {
    setCreateLoading(true);
    try {
      await createWorkspace({
        name: values.name,
        description: values.description || '',
        config: {
          embedding_model: values.embedding_model,
          chunk_size: values.chunk_size || 512,
          chunk_overlap: values.chunk_overlap || 64,
          query_mode: values.query_mode || 'hybrid',
          top_k: values.top_k || 5,
        },
      });
      message.success('知识库创建成功');
      setCreateOpen(false);
      form.resetFields();
      const res = await listWorkspaces();
      setWorkspaces(res.data.workspaces || []);
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '创建失败');
      }
    } finally {
      setCreateLoading(false);
    }
  }, [form]);

  const handleDelete = useCallback(async (name) => {
    try {
      await deleteWorkspace(name);
      message.success('知识库已删除');
      setWorkspaces(prev => prev.filter(w => w.name !== name));
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '删除失败');
      }
    }
  }, []);

  const filtered = workspaces.filter(w =>
    w.name.toLowerCase().includes(searchText.toLowerCase()) ||
    (w.description || '').toLowerCase().includes(searchText.toLowerCase())
  );

  return {
    workspaces: filtered, loading, isAdmin,
    createOpen, setCreateOpen, createLoading,
    searchText, setSearchText,
    form, navigate,
    handleCreate, handleDelete,
  };
};

export default useKnowledgePage;
