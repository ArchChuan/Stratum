import { Button, Divider, Drawer, Form, Input, Select, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { mechanismApi } from '../api/mechanism.api';
import type { Profile, ProfileBaseline, ProfileStatus, UpsertProfileInput } from '../model/mechanism';

import { PROMPT_TEXTAREA_MAX_LENGTH } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

const { Text } = Typography;
const { TextArea } = Input;

interface ProfileFormValues {
  display_name?: string;
  family_prefixes: string[];
  status?: ProfileStatus;
  memory_extraction?: string;
  memory_summary?: string;
  memory_enrichment?: string;
  memory_summarize?: string;
  memory_supersede?: string;
  compaction?: string;
  enrich_model?: string;
  summary_model?: string;
}

const PROMPT_FIELDS: { name: keyof ProfileBaseline; label: string; tooltip: string }[] = [
  { name: 'memory_extraction', label: '记忆提取模板', tooltip: 'llm_extractor 抽取模板，占位 %s/%s/%d' },
  { name: 'memory_summary', label: '记忆总结模板', tooltip: 'enricher 中文总结模板，占位 %s' },
  { name: 'memory_enrichment', label: '记忆富化模板', tooltip: 'enricher 富化模板，占位 %s/%s' },
  { name: 'memory_summarize', label: '周期总结模板', tooltip: 'history_summarizer 周期总结（无占位）' },
  { name: 'memory_supersede', label: '事实取代判断模板', tooltip: 'llm_superseder 判断模板，占位 %s/%s' },
  { name: 'compaction', label: '压缩指令模板', tooltip: 'history_compactor 压缩指令（无占位）' },
];

const toFormValues = (p: Profile): ProfileFormValues => ({
  display_name: p.display_name || undefined,
  family_prefixes: p.family_prefixes,
  status: p.status,
  memory_extraction: p.baseline?.memory_extraction || undefined,
  memory_summary: p.baseline?.memory_summary || undefined,
  memory_enrichment: p.baseline?.memory_enrichment || undefined,
  memory_summarize: p.baseline?.memory_summarize || undefined,
  memory_supersede: p.baseline?.memory_supersede || undefined,
  compaction: p.baseline?.compaction || undefined,
  enrich_model: p.baseline?.enrich_model || undefined,
  summary_model: p.baseline?.summary_model || undefined,
});

const toInput = (familyKey: string, v: ProfileFormValues): UpsertProfileInput => {
  const baseline: Partial<ProfileBaseline> = {};
  for (const field of PROMPT_FIELDS) {
    const value = v[field.name];
    if (value) baseline[field.name] = value;
  }
  if (v.enrich_model) baseline.enrich_model = v.enrich_model;
  if (v.summary_model) baseline.summary_model = v.summary_model;
  return {
    family_key: familyKey,
    display_name: v.display_name || undefined,
    family_prefixes: v.family_prefixes,
    status: v.status,
    baseline,
  };
};

interface ProfileEditDrawerProps {
  /** null = 新建档案；否则为待编辑档案。 */
  profile: Profile | null;
  open: boolean;
  onClose: () => void;
  onSaved: () => void;
}

export const ProfileEditDrawer = ({ profile, open, onClose, onSaved }: ProfileEditDrawerProps) => {
  const [form] = Form.useForm<ProfileFormValues>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      form.setFieldsValue(profile ? toFormValues(profile) : { family_prefixes: [] });
    }
  }, [open, profile, form]);

  const submit = async () => {
    let values: ProfileFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return; // 校验失败由 Form 展示
    }
    const familyKey = profile?.family_key ?? values.family_prefixes[0];
    setSaving(true);
    try {
      await mechanismApi.upsert(toInput(familyKey, values));
      message.success({ content: '模型档案已保存', duration: 2 });
      onSaved();
      onClose();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '保存模型档案失败'), duration: 0 });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      title={profile ? `编辑档案：${profile.family_key}` : '新建模型档案'}
      width={560}
      open={open}
      onClose={onClose}
      destroyOnClose
      extra={
        <span>
          {profile && (
            <Text type="secondary" style={{ marginRight: 12, fontSize: 12 }}>
              v{profile.version} · 指纹 {profile.fingerprint?.slice(0, 8)}
            </Text>
          )}
        </span>
      }
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Text type="secondary" style={{ marginRight: 'auto', fontSize: 12 }}>
            {profile
              ? '保存将升级版本（族键冲突覆盖），生效状态由「状态」决定'
              : '族键取首个家族前缀'}
          </Text>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={saving} onClick={() => void submit()}>
            保存档案
          </Button>
        </div>
      }
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="display_name"
          label="档案名称"
          rules={[{ max: 128, message: '名称不超过 128 字' }]}
        >
          <Input placeholder="如 通义千问族" />
        </Form.Item>
        <Form.Item
          name="family_prefixes"
          label="家族前缀"
          tooltip="模型名以任一前缀开头即命中本档案；首个前缀为新建档案的族键"
          rules={[{ required: true, message: '至少一个家族前缀' }]}
        >
          <Select mode="tags" placeholder="如 qwen，回车添加" open={false} suffixIcon={null} />
        </Form.Item>
        <Form.Item
          name="status"
          label="生效状态"
          tooltip="draft：仅建档不生效；active：消费路径立即使用"
        >
          <Select
            placeholder="默认 draft（仅建档）"
            options={[
              { value: 'active', label: 'active（生效）' },
              { value: 'draft', label: 'draft（建档）' },
            ]}
            allowClear={!profile}
          />
        </Form.Item>

        <Divider orientation="left" plain style={{ margin: '8px 0 16px' }}>
          管线模型引用
        </Divider>
        <Form.Item name="enrich_model" label="记忆富化模型" rules={[{ max: 128 }]}>
          <Input placeholder="如 qwen-max" />
        </Form.Item>
        <Form.Item name="summary_model" label="记忆总结模型" rules={[{ max: 128 }]}>
          <Input placeholder="如 qwen-max" />
        </Form.Item>

        <Divider orientation="left" plain style={{ margin: '8px 0 16px' }}>
          机制提示词模板（留空 = 使用种子默认）
        </Divider>
        {PROMPT_FIELDS.map((field) => (
          <Form.Item key={field.name} name={field.name} label={field.label} tooltip={field.tooltip}>
            <TextArea rows={3} placeholder="留空使用种子默认模板" maxLength={PROMPT_TEXTAREA_MAX_LENGTH} showCount />
          </Form.Item>
        ))}
      </Form>
    </Drawer>
  );
};

export default ProfileEditDrawer;
