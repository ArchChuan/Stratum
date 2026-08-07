import { ArrowLeftOutlined, SendOutlined } from '@ant-design/icons';
import { Alert, Button, Checkbox, Form, Input, Select, Skeleton, Space, Switch, Tabs, Typography, message } from 'antd';
import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { SkillRevision, SkillWorkspace } from '../model/skill';

import { SkillEvaluationPanel } from '@/modules/evaluation/components/SkillEvaluationPanel';
import { useEditorCandidates, useTenantRole } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

type CapabilityValues = { goal: string; whenToUse: string; inputSpec?: string; outputSpec?: string };
type ActivationValues = { name: string; description: string; inputSchemaJson: string; outputSchemaJson: string; confirmed: boolean };
type InstructionValues = { instructions: string; mcpToolIDs?: string; knowledgeWorkspaceIDs?: string; memoryScopes?: string[] };

export const SkillWorkspacePage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { isAdmin } = useTenantRole();
  const [workspace, setWorkspace] = useState<SkillWorkspace | null>(null);
  const [activeTab, setActiveTab] = useState('capability');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState('');
  const [error, setError] = useState('');
  const [editorIDs, setEditorIDs] = useState<string[]>([]);
  const { candidates: editorCandidates, loading: editorCandidatesLoading } = useEditorCandidates();
  const [capabilityForm] = Form.useForm<CapabilityValues>();
  const [activationForm] = Form.useForm<ActivationValues>();
  const [instructionForm] = Form.useForm<InstructionValues>();

  useEffect(() => {
    let cancelled = false;
    skillApi.getWorkspace(id).then((data) => {
      if (!cancelled) {
        setWorkspace(data);
        setEditorIDs(data.editors || []);
        fillForms(data.draft, capabilityForm, activationForm, instructionForm);
      }
    }).catch((err) => { if (!cancelled) setError(extractErrorMessage(err) || '加载技能工作台失败'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [id, capabilityForm, activationForm, instructionForm]);

  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (error) return <Alert type="error" message={error} showIcon />;
  if (!workspace) return <Alert type="warning" message="技能工作台不存在" showIcon />;
  const { skill, draft } = workspace;
  const canEdit = isAdmin && draft.status === 'draft';
  const activationConfirmed = Boolean(draft.activationContract.confirmed);
  const updateDraft = (next: SkillRevision) => { setWorkspace({ ...workspace, draft: next }); fillForms(next, capabilityForm, activationForm, instructionForm); };
  const perform = async (key: string, operation: () => Promise<SkillRevision>, success: string) => {
    setSaving(key);
    try { updateDraft(await operation()); message.success(success); }
    catch (err) { message.error({ content: extractErrorMessage(err) || '保存失败', duration: 0 }); }
    finally { setSaving(''); }
  };
  const publishDraft = async () => {
    setSaving('publish');
    try {
      await skillApi.publish(skill.id);
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '发布失败', duration: 0 });
      setSaving('');
      return;
    }
    try {
      const next = await skillApi.getWorkspace(skill.id);
      setWorkspace(next);
      fillForms(next.draft, capabilityForm, activationForm, instructionForm);
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
      { key: 'capability', label: '能力', children: <Form disabled={!canEdit} form={capabilityForm} layout="vertical" onFinish={(v) => perform('capability', () => skillApi.updateCapability(skill.id, v), '能力定义已保存')}>
        <Form.Item label="能力目标" name="goal" rules={[{ required: true }]}><TextArea rows={3} /></Form.Item>
        <Form.Item label="调用时机" name="whenToUse" rules={[{ required: true }]}><TextArea rows={3} /></Form.Item>
        <Form.Item label="输入说明" name="inputSpec"><TextArea rows={2} /></Form.Item>
        <Form.Item label="输出说明" name="outputSpec"><TextArea rows={2} /></Form.Item>
        {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'capability'}>保存能力</Button></ActionRow>}
      </Form> },
      { key: 'activation', label: '激活契约', children: <Form disabled={!canEdit} form={activationForm} layout="vertical" onFinish={(v) => perform('activation', () => skillApi.updateActivation(skill.id, {
        name: v.name, description: v.description, inputSchema: parseObject(v.inputSchemaJson, '输入 Schema'), outputSchema: parseObject(v.outputSchemaJson, '输出 Schema'), confirmed: v.confirmed,
      }), '激活契约已保存')}>
        <Form.Item label="激活名称" name="name" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item label="用途说明" name="description" rules={[{ required: true }]}><TextArea rows={3} /></Form.Item>
        <Form.Item label="输入 Schema" name="inputSchemaJson" rules={[{ required: true }]}><TextArea rows={6} /></Form.Item>
        <Form.Item label="输出 Schema" name="outputSchemaJson" rules={[{ required: true }]}><TextArea rows={6} /></Form.Item>
        <Form.Item label="确认契约" name="confirmed" valuePropName="checked"><Switch /></Form.Item>
        {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'activation'}>保存激活契约</Button></ActionRow>}
      </Form> },
      { key: 'instructions', label: '指令与权限', children: <Form disabled={!canEdit} form={instructionForm} layout="vertical" onFinish={(v) => perform('instructions', () => skillApi.updateInstructions(skill.id, {
        instructions: v.instructions, requirements: { mcpToolIds: lines(v.mcpToolIDs), knowledgeWorkspaceIds: lines(v.knowledgeWorkspaceIDs), memoryScopes: v.memoryScopes || [] },
      }), '指令与权限已保存')}>
        <Form.Item label="执行指令" name="instructions" rules={[{ required: true }]}><TextArea rows={10} /></Form.Item>
        <Form.Item label="MCP 工具 ID" name="mcpToolIDs" extra="一行一个，格式 mcp:<server>:<tool>"><TextArea rows={4} /></Form.Item>
        <Form.Item label="知识工作区 ID" name="knowledgeWorkspaceIDs"><TextArea rows={3} /></Form.Item>
        <Form.Item label="记忆范围" name="memoryScopes"><Checkbox.Group options={[{ label: '当前会话', value: 'conversation' }, { label: '用户', value: 'user' }, { label: 'Agent', value: 'agent' }]} /></Form.Item>
        {canEdit && <ActionRow><Button type="primary" htmlType="submit" loading={saving === 'instructions'}>保存指令与权限</Button></ActionRow>}
      </Form> },
      { key: 'editors', label: '可编辑人', children: (
        <div style={{ maxWidth: 520 }}>
          <Alert type="info" showIcon style={{ marginBottom: 16 }}
            message="可编辑人（租户管理员）可以修改此技能草稿；删除仍仅限创建者或超级管理员。" />
          <Select
            mode="multiple"
            placeholder="选择可编辑的管理员"
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
                disabled={!canEdit}
                onClick={async () => {
                  setSaving('editors');
                  try {
                    await skillApi.setEditors(skill.id, editorIDs);
                    message.success('编辑人已更新');
                  } catch (err) {
                    message.error({ content: extractErrorMessage(err) || '保存失败', duration: 0 });
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
        {canEdit && !activationConfirmed && <Alert type="warning" showIcon message="发布前需要确认激活契约。" action={<Button onClick={() => setActiveTab('activation')}>去确认激活契约</Button>} />}
        <Paragraph>Revision ID：{draft.id}<br />激活名称：{String(draft.activationContract.name || '')}</Paragraph>
        {canEdit && <ActionRow><Button disabled={!activationConfirmed} icon={<SendOutlined />} type="primary" loading={saving === 'publish'} onClick={publishDraft}>发布当前 Revision</Button></ActionRow>}
      </Space> },
      { key: 'evaluation', label: '评测与优化', children: <SkillEvaluationPanel skillId={skill.id} stableRevisionId={skill.activeRevisionId || (draft.status === 'published' ? draft.id : '')} isAdmin={isAdmin} /> },
    ]} />
  </div>;
};

const ActionRow = ({ children }: { children: ReactNode }) => <div className="responsive-form-actions" style={{ display: 'flex', justifyContent: 'flex-end' }}>{children}</div>;
const lines = (value?: string) => (value || '').split('\n').map((item) => item.trim()).filter(Boolean);
const stringify = (value: unknown) => JSON.stringify(value || {}, null, 2);
const parseObject = (raw: string, label: string): Record<string, unknown> => {
  try { const value = JSON.parse(raw); if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(); return value; }
  catch { throw new Error(`${label} 必须是合法 JSON 对象`); }
};
const fillForms = (draft: SkillRevision, capability: ReturnType<typeof Form.useForm<CapabilityValues>>[0], activation: ReturnType<typeof Form.useForm<ActivationValues>>[0], instructions: ReturnType<typeof Form.useForm<InstructionValues>>[0]) => {
  capability.setFieldsValue({ goal: String(draft.capability.goal || ''), whenToUse: String(draft.capability.whenToUse || ''), inputSpec: String(draft.capability.inputSpec || ''), outputSpec: String(draft.capability.outputSpec || '') });
  activation.setFieldsValue({ name: String(draft.activationContract.name || ''), description: String(draft.activationContract.description || ''), confirmed: Boolean(draft.activationContract.confirmed), inputSchemaJson: stringify(draft.activationContract.inputSchema || { type: 'object' }), outputSchemaJson: stringify(draft.activationContract.outputSchema || { type: 'object' }) });
  instructions.setFieldsValue({ instructions: draft.instructions, mcpToolIDs: draft.requirements.mcpToolIds.join('\n'), knowledgeWorkspaceIDs: draft.requirements.knowledgeWorkspaceIds.join('\n'), memoryScopes: draft.requirements.memoryScopes });
};

export default SkillWorkspacePage;
