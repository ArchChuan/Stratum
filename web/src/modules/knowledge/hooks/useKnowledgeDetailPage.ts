import { Form, message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { knowledgeApi } from '../api/knowledge.api';
import type {
  DocAccessValues,
  KnowledgeDocument,
  QueryResult,
  WorkspaceStats,
  WorkspaceVersion,
} from '../model/knowledge';

import { KNOWLEDGE_DEFAULT_TOP_K } from '@/constants';
import { tenantApi, useAuth, type Member } from '@/modules/iam';
import { extractErrorMessage, isForbidden } from '@/shared/lib';
import type { VersionRow } from '@/shared/ui';

const DOC_POLL_INTERVAL_MS = 5000;
// 租户角色候选：成员列表去重 + 兜底常见角色（白名单按 tenant_role 文本匹配）
const FALLBACK_ROLES = ['admin', 'owner', 'member'];

interface ConfigValues {
  query_mode?: string;
  chunk_size?: number;
  chunk_overlap?: number;
  top_k?: number;
  reranking?: string;
  rerank_model?: string;
  judge_model?: string;
  score_threshold?: number;
  rerank_top_k?: number;
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
  // 权限候选：全量租户成员 + 成员角色去重
  const [userCandidates, setUserCandidates] = useState<Member[]>([]);
  const [userCandidatesLoading, setUserCandidatesLoading] = useState(false);
  const [roleCandidates, setRoleCandidates] = useState<string[]>(FALLBACK_ROLES);
  // 文档访问权限弹窗（P0.8）
  const [editOpen, setEditOpen] = useState(false);
  const [editDoc, setEditDoc] = useState<KnowledgeDocument | null>(null);
  const [accessLoading, setAccessLoading] = useState(false);
  const [accessForm] = Form.useForm<DocAccessValues>();
  // 文档原文预览（P1.4）
  const [previewDoc, setPreviewDoc] = useState<KnowledgeDocument | null>(null);
  // 版本历史（仅 isAdmin 渲染入口；member 仅 GET，不可回滚）
  const [versions, setVersions] = useState<WorkspaceVersion[]>([]);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [versionsLoading, setVersionsLoading] = useState(false);

  // 加载权限候选（成员 + 角色），失败不阻塞页面
  useEffect(() => {
    let cancelled = false;
    setUserCandidatesLoading(true);
    tenantApi
      .members(1, 1000)
      .then((page) => {
        if (cancelled) return;
        setUserCandidates(page.members);
        // 角色候选始终并集完整角色集，避免小型租户因成员角色单一导致白名单下拉缺少选项。
        const roles = Array.from(
          new Set([...FALLBACK_ROLES, ...page.members.map((m) => m.role).filter(Boolean)]),
        );
        setRoleCandidates(roles);
      })
      .catch(() => {
        if (!cancelled) setUserCandidates([]);
      })
      .finally(() => {
        if (!cancelled) setUserCandidatesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const fetchDocuments = useCallback(async (): Promise<KnowledgeDocument[]> => {
    setDocumentsLoading(true);
    try {
      const docs = await knowledgeApi.listDocuments(name);
      setDocuments(docs);
      return docs;
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '获取文档列表失败', duration: 3 });
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
        reranking: data.config?.reranking,
        rerank_model: data.config?.rerank_model,
        judge_model: data.config?.judge_model,
        score_threshold: data.config?.score_threshold,
        rerank_top_k: data.config?.rerank_top_k,
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
      message.error({ content: extractErrorMessage(err) || '获取知识库详情失败', duration: 3 });
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
        message.error({ content: extractErrorMessage(err) || '更新失败', duration: 3 });
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
        message.error({ content: extractErrorMessage(err) || '更新失败', duration: 3 });
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
            reranking: values.reranking,
            score_threshold: values.score_threshold,
            rerank_top_k: values.rerank_top_k,
            // 仅 builtin 时随 Field 存在，否则 undefined → JSON 省略 → 后端 dormant 保留
            rerank_model: values.rerank_model,
            // 清空必须发 ""（allowClear 置 undefined 会被 JSON 丢弃 → 后端 partial 保留旧值 → 判断门关不掉）
            judge_model: values.judge_model ?? '',
          },
        });
        message.success({ content: '配置已保存', duration: 2 });
        fetchStats();
      } catch (err: unknown) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err) || '保存失败', duration: 3 });
        }
      } finally {
        setConfigLoading(false);
      }
    },
    [name, stats, fetchStats],
  );

  const handleUpload = useCallback(
    async ({ file, allowedUserIDs, allowedRoleIDs }: {
      file: File | Blob;
      allowedUserIDs?: string[];
      allowedRoleIDs?: string[];
    }) => {
      const formData = new FormData();
      formData.append('workspace', name);
      formData.append('file', file);
      setUploadLoading(true);
      try {
        // P0.8: 上传携带文档级白名单（platform-managed workspace 由后端忽略）
        const res = await knowledgeApi.ingest({
          formData,
          allowedUserIDs,
          allowedRoleIDs,
        });
        // 202 means ingest is now running in background; refresh docs so
        // the new row appears in 'processing' state and the polling effect
        // above takes over from here.
        fetchDocuments();
        const data = res.data as { total_chunks?: number; errors?: string[] };
        const totalChunks = data?.total_chunks ?? 0;
        const errs = data?.errors ?? [];
        if (errs.length > 0) {
          message.warning({ content: `上传完成，但存在错误：${errs[0]}`, duration: 3 });
        } else {
          message.success({ content: `上传成功，共 ${totalChunks} 个分块`, duration: 2 });
        }
        fetchStats();
      } catch (err: unknown) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err) || '上传失败', duration: 3 });
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
          message.error({ content: extractErrorMessage(err) || '查询失败', duration: 3 });
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
        message.error({ content: extractErrorMessage(err) || '删除文档失败', duration: 3 });
      } finally {
        setDeletingDocumentID('');
      }
    },
    [name, fetchDocuments, fetchStats],
  );

  // 打开权限弹窗并预填当前白名单（member 收到的回显恒为空，弹窗仅 admin 可见）
  const handleOpenAccess = useCallback((document: KnowledgeDocument) => {
    setEditDoc(document);
    setEditOpen(true);
    accessForm.setFieldsValue({
      allowedUserIDs: document.allowed_user_ids ?? [],
      allowedRoleIDs: document.allowed_role_ids ?? [],
    });
  }, [accessForm]);

  const handleSetDocAccess = useCallback(
    async (values: DocAccessValues) => {
      if (!editDoc) return;
      const allowedUserIDs = values.allowedUserIDs ?? [];
      const allowedRoleIDs = values.allowedRoleIDs ?? [];
      setAccessLoading(true);
      try {
        await knowledgeApi.setDocAccess(name, editDoc.id, { allowedUserIDs, allowedRoleIDs });
        message.success({ content: '访问权限已更新', duration: 2 });
        setEditOpen(false);
        await Promise.all([fetchDocuments(), fetchStats()]);
      } catch (err) {
        message.error({ content: extractErrorMessage(err) || '设置权限失败', duration: 3 });
      } finally {
        setAccessLoading(false);
      }
    },
    [name, editDoc, fetchDocuments, fetchStats],
  );

  const handlePreviewDocument = useCallback((document: KnowledgeDocument) => {
    // DocPreviewDrawer 自拉取预览内容，这里只记录目标
    setPreviewDoc(document);
  }, []);

  // 版本历史弹窗：打开即拉取最新列表；加载失败关闭弹窗并提示。
  const openVersions = useCallback(async () => {
    setVersionsOpen(true);
    setVersionsLoading(true);
    try {
      setVersions(await knowledgeApi.listVersions(name));
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载版本历史失败'), duration: 3 });
      setVersionsOpen(false);
    } finally {
      setVersionsLoading(false);
    }
  }, [name]);

  // 回滚由 VersionHistory 内置信确认 Modal 触发：此处只调 API + 刷新数据
  // （配置与版本列表），成功/失败提示在组件内兜底。
  const rollbackVersion = useCallback(async (row: VersionRow) => {
    await knowledgeApi.rollback(name, row.id);
    await Promise.all([fetchStats(), openVersions()]);
  }, [name, fetchStats, openVersions]);

  // 撤销未保存编辑（纯前端）：重拉最新 workspace 配置到 lastLoadedConfig，
  // 再整体回填表单，强制丢弃用户的未保存修改。
  const undoEdits = useCallback(async () => {
    await fetchStats();
    configForm.setFieldsValue({ ...lastLoadedConfig.current });
  }, [fetchStats, configForm, lastLoadedConfig]);

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
    userCandidates,
    userCandidatesLoading,
    roleCandidates,
    editOpen,
    setEditOpen,
    editDoc,
    accessLoading,
    accessForm,
    handleOpenAccess,
    handleSetDocAccess,
    previewDoc,
    setPreviewDoc,
    handlePreviewDocument,
    versions,
    versionsOpen,
    setVersionsOpen,
    versionsLoading,
    openVersions,
    rollbackVersion,
    undoEdits,
  };
};
