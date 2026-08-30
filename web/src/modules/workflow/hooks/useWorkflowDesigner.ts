import { message } from 'antd';
import { useCallback, useEffect, useReducer, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import { createInitialEditorState, workflowEditorReducer, type WorkflowEditorAction } from '../model/editor';
import { applyAutoLayout } from '../model/layout';
import type { WorkflowDefinition, WorkflowInputSchema } from '../model/workflow';

import { useAuth, useTenantRole } from '@/modules/iam';
import { useRequestEditorAccess } from '@/shared/hooks';
import { extractErrorMessage } from '@/shared/lib';

const emptyInputSchema: WorkflowInputSchema = { task_label: '', task_description: '', fields: [] };

export const useWorkflowDesigner = (workflowId?: string) => {
  const [editor, rawDispatch] = useReducer(workflowEditorReducer, undefined, () => createInitialEditorState());
  const [definition, setDefinition] = useState<WorkflowDefinition | null>(null);
  const [name, setNameState] = useState('');
  const [description, setDescriptionState] = useState('');
  const [inputSchema, setInputSchemaState] = useState<WorkflowInputSchema>(emptyInputSchema);
  const [detailsDirty, setDetailsDirty] = useState(false);
  const [loading, setLoading] = useState(Boolean(workflowId));
  const [saving, setSaving] = useState(false);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [validatedRevision, setValidatedRevision] = useState<number | null>(null);

  useEffect(() => {
    if (!workflowId) return undefined;
    let cancelled = false;
    setLoading(true);
    workflowApi.getWorkflow(workflowId).then((next) => {
      if (cancelled) return;
      setDefinition(next);
      setNameState(next.name);
      setDescriptionState(next.description);
      setInputSchemaState(next.input_schema);
      // 历史 spec 无 position：布局兜底一次（幂等，不覆盖已有坐标）
      rawDispatch({ type: 'server.reset', spec: applyAutoLayout(next.spec) });
      setDetailsDirty(false);
    }).catch((error: unknown) => {
      if (!cancelled) message.error({ content: extractErrorMessage(error, '操作失败'), duration: 3 });
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [workflowId]);

  const invalidateValidation = () => setValidatedRevision(null);
  const dispatch = useCallback((action: WorkflowEditorAction) => {
    rawDispatch(action);
    if (action.type !== 'selection.set' && action.type !== 'server.reset') invalidateValidation();
  }, []);
  const setName = (value: string) => { setNameState(value); setDetailsDirty(true); invalidateValidation(); };
  const setDescription = (value: string) => { setDescriptionState(value); setDetailsDirty(true); invalidateValidation(); };
  const setInputSchema = (value: WorkflowInputSchema) => { setInputSchemaState(value); setDetailsDirty(true); invalidateValidation(); };

  const save = async () => {
    setSaving(true);
    try {
      // 布局兜底作用于 payload：新插入的节点坐标缺位时也能落库，而非只作用于本地 reset
      const payload = { name, description, spec: applyAutoLayout(editor.spec), input_schema: inputSchema };
      const saved = definition
        ? await workflowApi.updateWorkflowDraft(definition.id, { ...payload, expected_revision: definition.revision })
        : await workflowApi.createWorkflow(payload);
      setDefinition(saved);
      setNameState(saved.name);
      setDescriptionState(saved.description);
      setInputSchemaState(saved.input_schema);
      rawDispatch({ type: 'server.reset', spec: applyAutoLayout(saved.spec) });
      setDetailsDirty(false);
      setValidatedRevision(null);
      message.success({ content: '草稿已保存', duration: 2 });
      return saved;
    } catch (error: unknown) {
      const isConflict = (error as { response?: { status?: number } })?.response?.status === 409;
      message.error({
        content: isConflict
          ? '草稿已被其他人修改，本地内容已保留，请刷新后重试'
          : extractErrorMessage(error, '操作失败'),
        duration: 3,
      });
      return null;
    } finally {
      setSaving(false);
    }
  };

  const validate = async () => {
    if (!definition || editor.dirty || detailsDirty) return false;
    setValidating(true);
    try {
      await workflowApi.validateWorkflow(definition.id);
      setValidatedRevision(definition.revision);
      message.success({ content: '当前修订校验通过', duration: 2 });
      return true;
    } catch (error: unknown) {
      message.error({ content: extractErrorMessage(error), duration: 3 });
      return false;
    } finally {
      setValidating(false);
    }
  };

  const publish = async () => {
    if (!definition || validatedRevision !== definition.revision) return null;
    setPublishing(true);
    try {
      const version = await workflowApi.publishWorkflow(definition.id);
      message.success({ content: '工作流已发布', duration: 2 });
      return version;
    } catch (error: unknown) {
      message.error({ content: extractErrorMessage(error), duration: 3 });
      return null;
    } finally {
      setPublishing(false);
    }
  };

  // P2 白名单：canEdit = admin/owner/白名单成员；新建页（无 id）恒可编辑（创建即 creator）。
  const { user } = useAuth();
  const { isAdmin, isOwner } = useTenantRole();
  const editors = definition?.editors ?? [];
  const canEdit = !workflowId || isAdmin || isOwner || editors.includes(user?.sub ?? '');
  const { requesting, request: requestEditor } = useRequestEditorAccess('workflow', workflowId ?? '', { resourceName: name });
  const saveEditors = async (editorIds: string[]): Promise<void> => {
    if (!workflowId) return;
    await workflowApi.setWorkflowEditors(workflowId, editorIds);
    message.success({ content: '可编辑人已更新', duration: 2 });
  };

  return {
    editor, dispatch, definition, name, description, inputSchema, setName, setDescription, setInputSchema,
    loading, saving, validating, publishing, dirty: editor.dirty || detailsDirty, validatedRevision, save, validate, publish,
    canEdit, editors, createdBy: definition?.created_by ?? '', isAdmin, isOwner, requesting, requestEditor, saveEditors,
  };
};
