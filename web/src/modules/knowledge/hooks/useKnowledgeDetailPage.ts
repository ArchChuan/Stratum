import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { knowledgeApi } from '../api/knowledge.api';
import type { KnowledgeDocument, QueryResult, WorkspaceStats } from '../model/knowledge';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const DOC_POLL_INTERVAL_MS = 5000;

interface ConfigValues {
  query_mode?: string;
  chunk_size?: number;
  chunk_overlap?: number;
  top_k?: number;
}

interface QueryValues {
  question: string;
  mode?: string;
  top_k?: number;
}

export const useKnowledgeDetailPage = () => {
  const { name = '' } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const [stats, setStats] = useState<WorkspaceStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [configForm] = Form.useForm<ConfigValues>();
  const [configLoading, setConfigLoading] = useState(false);
  const [uploadLoading, setUploadLoading] = useState(false);
  const [queryForm] = Form.useForm<QueryValues>();
  const [queryLoading, setQueryLoading] = useState(false);
  const [queryResult, setQueryResult] = useState<QueryResult | null>(null);
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [documentsLoading, setDocumentsLoading] = useState(false);
  const [deletingDocumentID, setDeletingDocumentID] = useState('');

  const fetchDocuments = useCallback(async (): Promise<KnowledgeDocument[]> => {
    setDocumentsLoading(true);
    try {
      const docs = await knowledgeApi.listDocuments(name);
      setDocuments(docs);
      return docs;
    } catch (err) {
      message.error(extractErrorMessage(err) || '获取文档列表失败');
      return [];
    } finally {
      setDocumentsLoading(false);
    }
  }, [name]);

  const fetchStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const data = await knowledgeApi.stats(name);
      setStats(data);
      configForm.setFieldsValue({
        chunk_size: data.config?.chunk_size,
        chunk_overlap: data.config?.chunk_overlap,
        query_mode: data.config?.query_mode,
        top_k: data.config?.top_k,
      });
    } catch (err) {
      message.error(extractErrorMessage(err) || '获取知识库详情失败');
    } finally {
      setStatsLoading(false);
    }
  }, [name, configForm]);

  useEffect(() => {
    fetchStats();
  }, [fetchStats]);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      if (cancelled) return;
      let docs: KnowledgeDocument[] = [];
      try {
        docs = await knowledgeApi.listDocuments(name);
        if (cancelled) return;
        setDocuments(docs);
      } catch {
        docs = [];
      }
      if (cancelled) return;
      const hasProcessing = docs.some((d) => d.ingest_status === 'processing');
      if (hasProcessing) {
        timer = setTimeout(tick, DOC_POLL_INTERVAL_MS);
      }
    };

    setDocumentsLoading(true);
    tick().finally(() => {
      if (!cancelled) setDocumentsLoading(false);
    });

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [name]);

  const handleNameSave = useCallback(
    async (newName: string) => {
      if (!newName || newName === name) return;
      try {
        await knowledgeApi.update(name, { name: newName });
        message.success('名称已更新');
        navigate(`/knowledge/${encodeURIComponent(newName)}`);
      } catch (err) {
        message.error(extractErrorMessage(err) || '更新失败');
      }
    },
    [name, navigate],
  );

  const handleDescriptionSave = useCallback(
    async (description: string) => {
      try {
        await knowledgeApi.update(name, { description });
        message.success('描述已更新');
        fetchStats();
      } catch (err) {
        message.error(extractErrorMessage(err) || '更新失败');
      }
    },
    [name, fetchStats],
  );

  const handleConfigSave = useCallback(
    async (values: ConfigValues) => {
      setConfigLoading(true);
      try {
        await knowledgeApi.update(name, {
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
      } catch (err: unknown) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) {
          message.error(extractErrorMessage(err) || '保存失败');
        }
      } finally {
        setConfigLoading(false);
      }
    },
    [name, stats, fetchStats],
  );

  const handleUpload = useCallback(
    async ({ file }: { file: File | Blob }) => {
      const formData = new FormData();
      formData.append('workspace', name);
      formData.append('file', file);
      setUploadLoading(true);
      try {
        const res = await knowledgeApi.ingest(formData);
        // 202 means ingest is now running in background; refresh docs so
        // the new row appears in 'processing' state and the polling effect
        // above takes over from here.
        fetchDocuments();
        const data = res.data as { total_chunks?: number; errors?: string[] };
        const totalChunks = data?.total_chunks ?? 0;
        const errs = data?.errors ?? [];
        if (errs.length > 0) {
          message.warning(`上传完成，但存在错误：${errs[0]}`);
        } else {
          message.success(`上传成功，共 ${totalChunks} 个分块`);
        }
        fetchStats();
      } catch (err: unknown) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status !== 403) {
          message.error(extractErrorMessage(err) || '上传失败');
        }
      } finally {
        setUploadLoading(false);
      }
      return false;
    },
    [name, fetchStats, fetchDocuments],
  );

  const handleQuery = useCallback(
    async (values: QueryValues) => {
      setQueryLoading(true);
      setQueryResult(null);
      try {
        const result = await knowledgeApi.query({
          question: values.question,
          workspace: name,
          mode: values.mode || stats?.config?.query_mode || 'hybrid',
          topK: values.top_k || stats?.config?.top_k || 5,
        });
        setQueryResult(result);
      } catch (err) {
        message.error(extractErrorMessage(err) || '查询失败');
      } finally {
        setQueryLoading(false);
      }
    },
    [name, stats],
  );

  const handleDeleteDocument = useCallback(
    async (documentID: string) => {
      setDeletingDocumentID(documentID);
      try {
        await knowledgeApi.deleteDocument(name, documentID);
        message.success('文档已删除');
        await Promise.all([fetchDocuments(), fetchStats()]);
      } catch (err) {
        message.error(extractErrorMessage(err) || '删除文档失败');
      } finally {
        setDeletingDocumentID('');
      }
    },
    [name, fetchDocuments, fetchStats],
  );

  return {
    name,
    navigate,
    isAdmin,
    stats,
    statsLoading,
    configForm,
    configLoading,
    uploadLoading,
    queryForm,
    queryLoading,
    queryResult,
    handleConfigSave,
    handleDescriptionSave,
    handleNameSave,
    handleUpload,
    handleQuery,
    documents,
    documentsLoading,
    deletingDocumentID,
    handleDeleteDocument,
    fetchDocuments,
  };
};
