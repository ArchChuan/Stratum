import { ArrowLeftOutlined, SendOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Select, Skeleton, Space, Tabs, Typography, message } from 'antd';
import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { SkillRevision, SkillWorkspace } from '../model/skill';

import { useAuth, useEditorCandidates, useTenantRole } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const { Title, Text, Paragraph } = Typography;
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
  const [error, setError] = useState('');
  const [editorIDs, setEditorIDs] = useState<string[]>([]);
  const { candidates: editorCandidates, loading: editorCandidatesLoading } = useEditorCandidates();
  const [draftForm] = Form.useForm<DraftValues>();

  useEffect(() => {
    let cancelled = false;
    skillApi.getWorkspace(id).then((data) => {
      if (!cancelled) {
        setWorkspace(data);
        setEditorIDs(data.editors || []);
        fillForms(data.draft, data.skill.name, data.skill.description, draftForm);
      }
    }).catch((err) => { if (!cancelled) setError(extractErrorMessage(err) || '加载技能工作台失败'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [id, draftForm]);

  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (error) return <Alert type="error" message={error} showIcon />;
  if (!workspace) return <Alert type="warning" message="技能工作台不存在" showIcon />;
  const { skill, draft } = workspace;
  // 白名单放宽：创建者必是 admin/owner（创建路由 requireAdmin），isAdmin 已覆盖创建者；
  // member 编辑者被加入可编辑人白名单后同样可编辑。
  const currentUserId = user?.sub || '';
  const canEdit = (isAdmin || (workspace.editors || []).includes(currentUserId)) && draft.status === 'draft';
  const updateDraft = (next: SkillRevision) => { setWorkspace({ ...workspace, draft: next }); fillForms(next, next.name || skill.name, next.description || skill.description, draftForm); };
  const perform = async (key: string, operation: () => Promise<SkillRevision>, success: string) => {
    setSaving(key);
    try { updateDraft(await operation()); message.success(success); }
    catch (err) { message.error({ content: extractErrorMessage(err) || '保存失败', duration: 3 }); }
    finally { setSaving(''); }
  };
  const publishDraft = async () => {
    setSaving('publish');
    try {
      await skillApi.publish(skill.id);
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '发布失败', duration: 3 });
      setSaving('');
      return;
    }
    try {
      const next = await skillApi.getWorkspace(skill.id);
      setWorkspace(next);
      fillForms(next.draft, next.skill.name, next.skill.description, draftForm);
      message.success({ content: 'Skill Revision 已发布', duration: 2 });
    } catch {
      setError('Revision 已发布，但工作台状态刷新失败。请重新进入页面确认最新状态。');
    } finally {
      setSaving('');
    }
  };

  return <div>
    <div className="responsive-detail-header" style={{ marginBottom: 20 }}>
      <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">返回</Button>
      <div className="long-text"><Title level={4} style={{ margin: 0 }}>{skill.name}</Title>
        <Text type="secondary">状态：{skill.status} · 草稿 Revision：{skill.draftRevisionId || '无'} · 当前 Revision：{skill.activeRevisionId || '未发布'}</Text>
      </div>
    </div>
    <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
      { key: 'instructions', label: '指令', children: <Form disabled={!canEdit} form={draftForm} layout="vertical" onFinish={(v) => perform('draft', () => skillApi.updateDraft(skill.id, v), '指令已保存')}>
        <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入技能名称' }]}><Input /></Form.Item>
        <Form.Item label="描述" name="description"><TextArea rows={3} /></Form.Item>
        <Form.Item label="执行指令" name="instructions" rules={[{ required: true, message: '请输入执行指令' }]}><TextArea rows={10} /></Form.Item>
        {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'draft'}>保存指令</Button></ActionRow>}
      </Form> },
      { key: 'editors', label: '可编辑人', children: (
        <div style={{ maxWidth: 520 }}>
          <Alert type="info" showIcon style={{ marginBottom: 16 }}
            message="白名单中的成员可编辑此技能草稿；删除仍仅限创建者或超级管理员。" />
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
                disabled={draft.status !== 'draft'}
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
      { key: 'revision', label: 'Revision', children: <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Alert type={draft.status === 'published' ? 'success' : 'warning'} showIcon message={draft.status === 'published' ? `已发布 Revision ${draft.revisionNo || 1}` : '发布后 Agent 才能激活此指令包。'} />
        <Paragraph>Revision ID：{draft.id}</Paragraph>
        {canEdit && <ActionRow><Button icon={<SendOutlined />} type="primary" loading={saving === 'publish'} onClick={publishDraft}>发布当前 Revision</Button></ActionRow>}
      </Space> },
    ]} />
  </div>;
};

const ActionRow = ({ children }: { children: ReactNode }) => <div className="responsive-form-actions" style={{ display: 'flex', justifyContent: 'flex-end' }}>{children}</div>;
const fillForms = (draft: SkillRevision, skillName: string, skillDescription: string, draftForm: ReturnType<typeof Form.useForm<DraftValues>>[0]) => {
  draftForm.setFieldsValue({ name: draft.name || skillName, description: draft.description || skillDescription, instructions: draft.instructions });
};

export default SkillWorkspacePage;
