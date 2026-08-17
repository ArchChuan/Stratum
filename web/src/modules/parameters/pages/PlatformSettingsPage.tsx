import {
  Button,
  Card,
  Col,
  Form,
  Row,
  Typography,
  message,
} from 'antd';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { parametersApi } from '../api/parameters.api';
import { ParameterControl } from '../components/ParameterControl';
import { PromptDefaultViewer } from '../components/PromptDefaultViewer';
import type {
  ParameterDefinition,
  PlatformSettingsFormValues,
  PlatformValues,
} from '../model/parameters';

import { extractErrorMessage, isForbidden } from '@/shared/lib';
import { DefaultHint } from '@/shared/ui';

const { Text } = Typography;

// 敏感参数(apiKey 类)永不渲染;资源 scope 由 Agent / 知识库表单各自承载。
const renderable = (def: ParameterDefinition): boolean =>
  def.scope === 'platform' && !def.sensitive;

const groupByCategory = (defs: ParameterDefinition[]): ParameterDefinition[] =>
  defs.filter(renderable);

// 平台页可查看默认模板的提示词键（与后端 prompt-defaults 白名单对应，
// agent.* 与 memory.extraction_prompt 在 Agent 编辑页展示）。
const PROMPT_DEFAULT_KEYS = new Set([
  'memory.enrich_prompt',
  'memory.summary_prompt',
  'memory.history_summary_prompt',
  'memory.supersede_prompt',
]);

// PlatformFieldItem 逐字段 watch 派生 unset（只重渲染本字段）。unset = 表单无值
// 或 0/''（List 语义：非 0 默认后端已回填，缺失键即 0/''/nil 默认）。hint 只
// 提示不写回；toggle 恒被 List 返回，无缺失场景，不渲染 hint。
const PlatformFieldItem = ({ def }: { def: ParameterDefinition }) => {
  const form = Form.useFormInstance();
  const value = Form.useWatch(def.key, form);
  const isToggle = def.visual_hint.control === 'toggle';
  const unset = value == null || value === '' || value === 0;

  let hint: ReactNode = null;
  if (!isToggle && unset) {
    const d = def.default;
    if (d === undefined || d === null || d === '') {
      hint = <Text type="secondary">未设置（使用定义默认）</Text>;
    } else if (typeof d === 'number' && d === 0) {
      hint = <DefaultHint value={0} suffix="（未设置）" />;
    } else {
      hint = <DefaultHint value={d} />;
    }
  }

  return (
    <Form.Item
      label={def.display_name || def.key}
      name={def.key}
      valuePropName={isToggle ? 'checked' : undefined}
      tooltip={def.description || def.key}
      extra={
        hint !== null || (def.visual_hint.control === 'textarea' && PROMPT_DEFAULT_KEYS.has(def.key)) ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            {hint}
            {def.visual_hint.control === 'textarea' && PROMPT_DEFAULT_KEYS.has(def.key) && (
              <PromptDefaultViewer promptKey={def.key} />
            )}
          </div>
        ) : undefined
      }
    >
      <ParameterControl def={def} />
    </Form.Item>
  );
};

export const PlatformSettingsPage = () => {
  const [form] = Form.useForm<PlatformSettingsFormValues>();
  const [defs, setDefs] = useState<ParameterDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [schemaRes, valuesRes] = await Promise.all([
          parametersApi.schema(),
          parametersApi.list(),
        ]);
        if (cancelled) return;
        setDefs(schemaRes);
        const initial: PlatformSettingsFormValues = {};
        for (const def of schemaRes.filter(renderable)) {
          if (valuesRes[def.key] !== undefined) {
            initial[def.key] = valuesRes[def.key] as number | string | boolean;
          }
        }
        form.setFieldsValue(initial);
      } catch (err) {
        if (!cancelled) {
          message.error({ content: extractErrorMessage(err, '加载平台参数失败'), duration: 0 });
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [form]);

  const groups = useMemo(() => {
    const byCategory = new Map<string, ParameterDefinition[]>();
    for (const def of groupByCategory(defs)) {
      const cat = def.category || '其他';
      const list = byCategory.get(cat) ?? [];
      list.push(def);
      byCategory.set(cat, list);
    }
    return Array.from(byCategory.entries());
  }, [defs]);

  const onFinish = useCallback(
    async (formValues: PlatformSettingsFormValues) => {
      setSaving(true);
      const patch: PlatformValues = {};
      for (const def of defs.filter(renderable)) {
        const v = formValues[def.key];
        if (v === undefined || v === null) continue;
        // 模型 key 等于定义默认时跳过提交：默认值由后端 resolver 兜底（DB 留空），
        // 避免把未修改的默认模型名重复写库并在目录缺失时触发 ValidateFn 400。
        if (def.visual_hint.control === 'model' && v === def.default) continue;
        patch[def.key] = v;
      }
      try {
        await parametersApi.update(patch);
        message.success({ content: '平台参数已保存', duration: 2 });
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存平台参数失败'), duration: 0 });
        }
      } finally {
        setSaving(false);
      }
    },
    [defs],
  );

  return (
    <div style={{ maxWidth: 960, margin: '0 auto', padding: '24px 16px' }}>
      <div style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ marginBottom: 4 }}>
          平台参数
        </Typography.Title>
        <Text type="secondary">
          全局可优化参数与提示词配置。0 = 未设置（使用定义默认）；资源级参数在 Agent / 知识库表单中各自配置。
        </Text>
      </div>

      <Form form={form} layout="vertical" onFinish={onFinish}>
        {groups.map(([category, defsInGroup]) => (
          <Card
            key={category}
            title={category}
            loading={loading}
            style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
          >
            <Row gutter={[24, 8]}>
              {defsInGroup.map((def) => (
                <Col key={def.key} xs={24} md={12}>
                  <PlatformFieldItem def={def} />
                </Col>
              ))}
            </Row>
          </Card>
        ))}

        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={saving}>
            保存平台参数
          </Button>
        </Form.Item>
      </Form>
    </div>
  );
};
