import { ArrowLeftOutlined, LockOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Select, Skeleton, Tabs, Typography, message } from 'antd';
import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { SkillRevision, SkillWorkspace } from '../model/skill';

import { useAuth, useEditorCandidates, useTenantRole } from '@/modules/iam';
import { operationProposalApi } from '@/modules/operation-gate';
import { extractErrorMessage, isForbidden } from '@/shared/lib';
import { VersionHistory, type VersionRow } from '@/shared/ui';

const { Title, Text } = Typography;
const { TextArea } = Input;

type DraftValues = { name: string; description: string; instructions: string };

export const SkillWorkspacePage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { isAdmin } = useTenantRole();
  const { user } = useAuth();
  const [workspace, setWorkspace] = useState<SkillWorkspace | null>(null);
  const [activeTab, setActiveTab] = useState('instructions');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState('');
  const [requesting, setRequesting] = useState(false);
  const [error, setError] = useState('');
  const [editorIDs, setEditorIDs] = useState<string[]>([]);
  const [refreshTick, setRefreshTick] = useState(0);
  const [revisions, setRevisions] = useState<SkillRevision[]>([]);
  const [revisionsLoading, setRevisionsLoading] = useState(false);
  const { candidates: editorCandidates, loading: editorCandidatesLoading } = useEditorCandidates();
  const [draftForm] = Form.useForm<DraftValues>();

  useEffect(() => {
    let cancelled = false;
    skillApi.getWorkspace(id).then((data) => {
      if (!cancelled) {
        setWorkspace(data);
        setEditorIDs(data.editors || []);
        fillForms(data.active, data.skill.name, data.skill.description, draftForm);
      }
    }).catch((err) => { if (!cancelled) setError(extractErrorMessage(err) || '加载技能工作台失败'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [id, draftForm]);

  // 版本历史：进入该 tab 才加载；保存/回滚后 bump refreshTick 触发重拉。
  useEffect(() => {
    if (activeTab !== 'revision') return;
    let cancelled = false;
    setRevisionsLoading(true);
    skillApi.listRevisions(id).then((rows) => { if (!cancelled) setRevisions(rows ?? []); })
      .catch((err) => { if (!cancelled && !isForbidden(err)) message.error({ content: extractErrorMessage(err) || '加载版本历史失败', duration: 3 }); })
      .finally(() => { if (!cancelled) setRevisionsLoading(false); });
    return () => { cancelled = true; };
  }, [activeTab, id, refreshTick]);

  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (error) return <Alert type="error" message={error} showIcon />;
  if (!workspace) return <Alert type="warning" message="技能工作台不存在" showIcon />;
  const { skill, active } = workspace;
  const currentUserId = user?.sub || '';
  // 白名单放宽：创建者必是 admin/owner（创建路由 requireAdmin），isAdmin 已覆盖创建者；
  // member 编辑者被加入可编辑人白名单后同样可编辑。
  const canEdit = isAdmin || (workspace.editors || []).includes(currentUserId);
  const applyWorkspace = (next: SkillWorkspace) => {
    setWorkspace(next);
    fillForms(next.active, next.skill.name, next.skill.description, draftForm);
  };
  // saveRevision: 保存即生效——派生新版本并立即生效，无发布步骤。乐观并发基线
  // expectedContentHash 取当前生效版本内容 hash，并发编辑时后端返回 409。
  const saveRevision = async (values: DraftValues) => {
    setSaving('draft');
    try {
      const next = await skillApi.updateSkill(skill.id, {
        name: values.name, description: values.description, instructions: values.instructions,
        expectedContentHash: active.contentHash,
      });
      applyWorkspace(next);
      setRefreshTick((t) => t + 1);
      message.success({ content: '已保存并立即生效', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '保存失败', duration: 3 });
    } finally {
      setSaving('');
    }
  };
  const reloadWorkspace = async () => {
    try {
      applyWorkspace(await skillApi.getWorkspace(id));
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '刷新工作台失败', duration: 3 });
    }
  };
  // 回滚：生效指针指回历史版本，立即生效、不产生新版本；成功后重拉工作台与版本历史。
  const handleRollback = async (row: VersionRow) => {
    await skillApi.rollback(skill.id, row.id);
    await reloadWorkspace();
    setRefreshTick((t) => t + 1);
  };
  // 普通成员（非白名单）申请编辑权限 → grant_editor 提案，管理员审批后即授予。
  const handleRequestEditor = async () => {
    setRequesting(true);
    try {
      await operationProposalApi.requestEditorAccess('skill', skill.id, { resourceName: skill.name });
      message.success({ content: '已提交，等待管理员审批', duration: 3 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '提交申请失败'), duration: 3 });
    } finally {
      setRequesting(false);
    }
  };

  return <div>
    <div className="responsive-detail-header" style={{ marginBottom: 20 }}>
      <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">返回</Button>
      <div className="long-text"><Title level={4} style={{ margin: 0 }}>{skill.name}</Title>
        <Text type="secondary">状态：{skill.status} · 当前版本：v{active.revisionNo ?? '—'} · Revision：{skill.activeRevisionId || '未发布'}{!canEdit && ' · 只读，可申请编辑权限'}</Text>
      </div>
    </div>
    <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
      // 申请编辑权限按钮放在 Form 外：<Form disabled={!canEdit}> 会通过 DisabledContext
      // 禁用表单内所有 antd 组件（含 Button），member 只读时必须可点申请。
      { key: 'instructions', label: '指令', children: <div>
        <Form disabled={!canEdit} form={draftForm} layout="vertical" onFinish={saveRevision}>
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入技能名称' }]}><Input /></Form.Item>
          <Form.Item label="描述" name="description"><TextArea rows={3} /></Form.Item>
          <Form.Item label="执行指令" name="instructions" rules={[{ required: true, message: '请输入执行指令' }]}><TextArea rows={10} /></Form.Item>
          {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'draft'}>保存并立即生效</Button></ActionRow>}
        </Form>
        {!canEdit && <ActionRow><Button type="primary" icon={<LockOutlined />} loading={requesting} onClick={() => void handleRequestEditor()}>申请编辑权限</Button></ActionRow>}
      </div> },
      { key: 'editors', label: '可编辑人', children: (
        <div style={{ maxWidth: 520 }}>
          <Alert type="info" showIcon style={{ marginBottom: 16 }}
            message="白名单中的成员可编辑此技能；删除仍仅限创建者或超级管理员。" />
          <Select
            mode="multiple"
            placeholder="选择可编辑的租户成员"
            allowClear
            loading={editorCandidatesLoading}
            value={editorIDs}
            onChange={setEditorIDs}
            style={{ width: '100%' }}
            options={editorCandidates.map((member) => ({
              value: member.user_id,
              label: member.github_login || member.user_id,
            }))}
          />
          {isAdmin && (
            <ActionRow>
              <Button
                type="primary"
                loading={saving === 'editors'}
                onClick={async () => {
                  setSaving('editors');
                  try {
                    await skillApi.setEditors(skill.id, editorIDs);
                    message.success({ content: '编辑人已更新', duration: 2 });
                  } catch (err) {
                    message.error({ content: extractErrorMessage(err) || '保存失败', duration: 3 });
                  } finally {
                    setSaving('');
                  }
                }}
              >
                保存编辑人
              </Button>
            </ActionRow>
          )}
        </div>
      ) },
      { key: 'revision', label: '版本历史', children: (
        <VersionHistory
          rows={(revisions ?? []).map((r) => ({
            id: r.id, versionNo: r.revisionNo, status: r.status, isCurrent: r.isCurrent,
            createdByName: r.createdByName, createdBy: r.createdBy, createdAt: r.createdAt,
            canRollback: r.status === 'deprecated' && canEdit,
          }))}
          loading={revisionsLoading}
          rollback={handleRollback}
          infoMessage="保存即产生新版本并立即生效；历史版本可回滚，回滚不产生新版本。"
        />
      ) },
    ]} />
  </div>;
};

const ActionRow = ({ children }: { children: ReactNode }) => <div className="responsive-form-actions" style={{ display: 'flex', justifyContent: 'flex-end' }}>{children}</div>;
const fillForms = (active: SkillRevision, skillName: string, skillDescription: string, draftForm: ReturnType<typeof Form.useForm<DraftValues>>[0]) => {
  draftForm.setFieldsValue({ name: active.name || skillName, description: active.description || skillDescription, instructions: active.instructions });
};

export default SkillWorkspacePage;
