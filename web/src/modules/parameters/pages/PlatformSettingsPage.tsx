import {
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Row,
  Select,
  Slider,
  Switch,
  Typography,
  message,
} from 'antd';
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';

import { parametersApi } from '../api/parameters.api';
import type {
  ParameterDefinition,
  PlatformSettingsFormValues,
  PlatformValues,
  VisualHint,
} from '../model/parameters';

import { extractErrorMessage, isForbidden } from '@/shared/lib';

const { Text } = Typography;
const { TextArea } = Input;
const { Option } = Select;

// 敏感参数(apiKey 类)永不渲染;资源 scope 由 Agent / 知识库表单各自承载。
const renderable = (def: ParameterDefinition): boolean =>
  def.scope === 'platform' && !def.sensitive;

const groupByCategory = (defs: ParameterDefinition[]): ParameterDefinition[] =>
  defs.filter(renderable);

const sliderMarks = (hint: VisualHint): Record<number, string> => {
  if (hint.max == null) return {};
  return {
    [hint.min ?? 0]: String(hint.min ?? 0),
    [hint.max]: String(hint.max),
  };
};

const controlFor = (def: ParameterDefinition): ReactNode => {
  const hint: VisualHint = def.visual_hint;
  switch (hint.control) {
    case 'toggle':
      return <Switch />;
    case 'select': {
      const options = (hint.options ?? []).map((opt) => String(opt));
      return (
        <Select
          style={{ width: '100%', maxWidth: 240 }}
          allowClear
          placeholder="未设置（使用定义默认）"
        >
          {options.map((opt) => (
            <Option key={opt} value={opt}>
              {opt}
            </Option>
          ))}
        </Select>
      );
    }
    case 'slider':
      return <Slider min={hint.min ?? 0} max={hint.max ?? 100} step={hint.step ?? 1} marks={sliderMarks(hint)} />;
    case 'number':
      return (
        <InputNumber
          min={hint.min ?? 0}
          max={hint.max ?? undefined}
          step={hint.step ?? 1}
          style={{ width: '100%', maxWidth: 240 }}
        />
      );
    case 'textarea':
      return (
        <TextArea
          rows={4}
          placeholder={typeof def.default === 'string' ? `默认：${def.default}` : undefined}
        />
      );
    default:
      return null;
  }
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
                  <Form.Item
                    label={def.display_name || def.key}
                    name={def.key}
                    valuePropName={def.visual_hint.control === 'toggle' ? 'checked' : undefined}
                    tooltip={def.description || def.key}
                  >
                    {controlFor(def)}
                  </Form.Item>
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
