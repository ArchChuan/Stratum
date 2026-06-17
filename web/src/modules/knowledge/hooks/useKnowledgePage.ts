import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { knowledgeApi } from '../api/knowledge.api';
import type { CreateWorkspaceInput, Workspace } from '../model/knowledge';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

interface CreateValues {
  name: string;
  description?: string;
  embedding_model: string;
  chunk_size?: number;
  chunk_overlap?: number;
  query_mode?: string;
  top_k?: number;
}

export const useKnowledgePage = () => {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [searchText, setSearchText] = useState('');
  const [form] = Form.useForm<CreateValues>();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const list = await knowledgeApi.list();
        if (!cancelled) setWorkspaces(list);
      } catch (err) {
        if (!cancelled) message.error(extractErrorMessage(err) || '获取知识库列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const handleCreate = useCallback(
    async (values: CreateValues) => {
      setCreateLoading(true);
      try {
        const payload: CreateWorkspaceInput = {
          name: values.name,
          description: values.description || '',
          config: {
            embedding_model: values.embedding_model,
            chunk_size: values.chunk_size || 512,
            chunk_overlap: values.chunk_overlap || 64,
            query_mode: values.query_mode || 'hybrid',
            top_k: values.top_k || 5,
          },
        };
        await knowledgeApi.create(payload);
        message.success('知识库创建成功');
        setCreateOpen(false);
        form.resetFields();
        setWorkspaces(await knowledgeApi.list());
      } catch (err: unknown) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) {
          message.error(extractErrorMessage(err) || '创建失败');
        }
      } finally {
        setCreateLoading(false);
      }
    },
    [form],
  );

  const handleDelete = useCallback(async (name: string) => {
    try {
      await knowledgeApi.delete(name);
      message.success('知识库已删除');
      setWorkspaces((prev) => prev.filter((w) => w.name !== name));
    } catch (err: unknown) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status !== 403) {
        message.error(extractErrorMessage(err) || '删除失败');
      }
    }
  }, []);

  const filtered = workspaces.filter(
    (w) =>
      w.name.toLowerCase().includes(searchText.toLowerCase()) ||
      (w.description || '').toLowerCase().includes(searchText.toLowerCase()),
  );

  return {
    workspaces: filtered,
    loading,
    isAdmin,
    createOpen,
    setCreateOpen,
    createLoading,
    searchText,
    setSearchText,
    form,
    navigate,
    handleCreate,
    handleDelete,
  };
};
