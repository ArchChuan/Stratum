import { ArrowLeftOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Modal, Select, Skeleton, Table, Tabs, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { SkillRevision, SkillWorkspace } from '../model/skill';

import { useAuth, useEditorCandidates, useTenantRole } from '@/modules/iam';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

type DraftValues = { name: string; description: string; instructions: string };

// 版本状态 → 标签展示。published 是当前生效版本，deprecated 是被新版本覆盖的
// 历史版本（可回滚），candidate 由评测优化产生。
const REVISION_STATUS_TAG: Record<string, { color: string; label: string }> = {
  published: { color: 'green', label: '已发布' },
  deprecated: { color: 'default', label: '历史' },
  candidate: { color: 'purple', label: '评测' },
};

export const SkillWorkspacePage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { isAdmin } = useTenantRole();
  const { user } = useAuth();
  const [workspace, setWorkspace] = useState<SkillWorkspace | null>(null);
  const [activeTab, setActiveTab] = useState('instructions');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState('');
  const [error, setError] = useState('');
  const [editorIDs, setEditorIDs] = useState<string[]>([]);
  const [refreshTick, setRefreshTick] = useState(0);
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

  return <div>
    <div className="responsive-detail-header" style={{ marginBottom: 20 }}>
      <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">返回</Button>
      <div className="long-text"><Title level={4} style={{ margin: 0 }}>{skill.name}</Title>
        <Text type="secondary">状态：{skill.status} · 当前版本：v{active.revisionNo ?? '—'} · Revision：{skill.activeRevisionId || '未发布'}</Text>
      </div>
    </div>
    <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
      { key: 'instructions', label: '指令', children: <Form disabled={!canEdit} form={draftForm} layout="vertical" onFinish={saveRevision}>
        <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入技能名称' }]}><Input /></Form.Item>
        <Form.Item label="描述" name="description"><TextArea rows={3} /></Form.Item>
        <Form.Item label="执行指令" name="instructions" rules={[{ required: true, message: '请输入执行指令' }]}><TextArea rows={10} /></Form.Item>
        {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'draft'}>保存并立即生效</Button></ActionRow>}
      </Form> },
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
        <SkillVersionHistory skillId={skill.id} canEdit={canEdit} refreshTick={refreshTick} onRolledBack={reloadWorkspace} />
      ) },
    ]} />
  </div>;
};

// SkillVersionHistory 展示版本的版本历史：当前生效标记、操作者、时间与回滚入口。
// 回滚将生效指针指回历史已发布版本，立即生效、不产生新版本。
const SkillVersionHistory = ({ skillId, canEdit, refreshTick, onRolledBack }: {
  skillId: string; canEdit: boolean; refreshTick: number; onRolledBack: () => void;
}) => {
  const [revisions, setRevisions] = useState<SkillRevision[]>([]);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    skillApi.listRevisions(skillId).then((rows) => { if (!cancelled) setRevisions(rows); })
      .catch((err) => { if (!cancelled && !isForbidden(err)) message.error({ content: extractErrorMessage(err) || '加载版本历史失败', duration: 3 }); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [skillId, refreshTick]);

  const rollback = (revision: SkillRevision) => {
    Modal.confirm({
      title: `回滚到版本 v${revision.revisionNo ?? '—'}？`,
      content: '回滚后该版本立即生效：当前版本标记为历史，不产生新版本，历史保留可再次回滚。',
      okText: '回滚', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        try {
          await skillApi.rollback(skillId, revision.id);
          message.success({ content: `已回滚到版本 v${revision.revisionNo ?? '—'}`, duration: 2 });
          onRolledBack();
        } catch (err) {
          message.error({ content: extractErrorMessage(err) || '回滚失败', duration: 3 });
        }
      },
    });
  };

  const columns: ColumnsType<SkillRevision> = [
    { title: '版本', dataIndex: 'revisionNo', width: 80, render: (no: number) => `v${no ?? '—'}` },
    { title: '状态', dataIndex: 'status', width: 150, render: (_: unknown, r: SkillRevision) => (
      <>
        {r.isCurrent && <Tag color="blue" style={{ marginInlineEnd: 4 }}>当前生效</Tag>}
        <Tag color={REVISION_STATUS_TAG[r.status]?.color}>{REVISION_STATUS_TAG[r.status]?.label ?? r.status}</Tag>
      </>
    ) },
    { title: '操作者', dataIndex: 'createdBy', width: 120, render: (actor: string) => actor || <Text type="secondary">—</Text> },
    { title: '时间', dataIndex: 'createdAt', width: 180, render: (t: string) => (t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : <Text type="secondary">—</Text>) },
    { title: '操作', key: 'actions', width: 100, render: (_: unknown, r: SkillRevision) => (
      r.status === 'deprecated' && canEdit ? (
        <Button type="link" size="small" danger onClick={() => rollback(r)}>回滚</Button>
      ) : null
    ) },
  ];

  return (
    <div style={{ maxWidth: 720 }}>
      <Alert type="info" showIcon style={{ marginBottom: 16 }}
        message="保存即产生新版本并立即生效；历史版本可回滚，回滚不产生新版本。" />
      <Table<SkillRevision> rowKey="id" size="small" loading={loading} columns={columns} dataSource={revisions}
        pagination={{ pageSize: 5, showSizeChanger: false }}
        locale={{ emptyText: <Paragraph type="secondary" style={{ padding: 16 }}>暂无版本记录</Paragraph> }} />
    </div>
  );
};

const ActionRow = ({ children }: { children: ReactNode }) => <div className="responsive-form-actions" style={{ display: 'flex', justifyContent: 'flex-end' }}>{children}</div>;
const fillForms = (active: SkillRevision, skillName: string, skillDescription: string, draftForm: ReturnType<typeof Form.useForm<DraftValues>>[0]) => {
  draftForm.setFieldsValue({ name: active.name || skillName, description: active.description || skillDescription, instructions: active.instructions });
};

export default SkillWorkspacePage;
