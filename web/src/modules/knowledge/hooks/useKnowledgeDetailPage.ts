import { Form, message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { knowledgeApi } from '../api/knowledge.api';
import type { KnowledgeDocument, QueryResult, WorkspaceStats } from '../model/knowledge';

import { KNOWLEDGE_DEFAULT_TOP_K } from '@/constants';
import { useAuth } from '@/modules/iam';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

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
  const lastLoadedConfig = useRef<ConfigValues>({});
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
      message.error({ content: extractErrorMessage(err) || '获取文档列表失败', duration: 0 });
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
      const values: ConfigValues = {
        chunk_size: data.config?.chunk_size,
        chunk_overlap: data.config?.chunk_overlap,
        query_mode: data.config?.query_mode,
        top_k: data.config?.top_k,
      };
      for (const field of Object.keys(values) as (keyof ConfigValues)[]) {
        const currentValue = configForm.getFieldValue(field);
        const lastLoadedValue = lastLoadedConfig.current[field];
        if (lastLoadedValue === undefined || Object.is(currentValue, lastLoadedValue)) {
          configForm.setFieldValue(field, values[field]);
        }
      }
      lastLoadedConfig.current = values;
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '获取知识库详情失败', duration: 0 });
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
        message.success({ content: '名称已更新', duration: 2 });
        navigate(`/knowledge/${encodeURIComponent(newName)}`);
      } catch (err) {
        message.error({ content: extractErrorMessage(err) || '更新失败', duration: 0 });
      }
    },
    [name, navigate],
  );

  const handleDescriptionSave = useCallback(
    async (description: string) => {
      try {
        await knowledgeApi.update(name, { description });
        message.success({ content: '描述已更新', duration: 2 });
        fetchStats();
      } catch (err) {
        message.error({ content: extractErrorMessage(err) || '更新失败', duration: 0 });
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
        message.success({ content: '配置已保存', duration: 2 });
        fetchStats();
      } catch (err: unknown) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err) || '保存失败', duration: 0 });
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
          message.warning({ content: `上传完成，但存在错误：${errs[0]}`, duration: 0 });
        } else {
          message.success({ content: `上传成功，共 ${totalChunks} 个分块`, duration: 2 });
        }
        fetchStats();
      } catch (err: unknown) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err) || '上传失败', duration: 0 });
        }
      } finally {
        setUploadLoading(false);
      }
      return false;
    },
    [name, fetchStats, fetchDocuments],
  );

  // 查询请求序号防竞态：连续查询时旧响应不覆盖新结果（M5）
  const queryGenRef = useRef(0);
  const handleQuery = useCallback(
    async (values: QueryValues) => {
      const gen = ++queryGenRef.current;
      setQueryLoading(true);
      setQueryResult(null);
      try {
        const result = await knowledgeApi.query({
          question: values.question,
          workspace: name,
          mode: values.mode || stats?.config?.query_mode || 'hybrid',
          topK: values.top_k || stats?.config?.top_k || KNOWLEDGE_DEFAULT_TOP_K,
        });
        if (gen !== queryGenRef.current) return;
        setQueryResult(result);
      } catch (err) {
        if (gen === queryGenRef.current) {
          message.error({ content: extractErrorMessage(err) || '查询失败', duration: 0 });
        }
      } finally {
        if (gen === queryGenRef.current) setQueryLoading(false);
      }
    },
    [name, stats],
  );

  const handleDeleteDocument = useCallback(
    async (documentID: string) => {
      setDeletingDocumentID(documentID);
      try {
        await knowledgeApi.deleteDocument(name, documentID);
        message.success({ content: '文档已删除', duration: 2 });
        await Promise.all([fetchDocuments(), fetchStats()]);
      } catch (err) {
        message.error({ content: extractErrorMessage(err) || '删除文档失败', duration: 0 });
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
