import { Button, Col, Form, Row, Skeleton, Tabs, Typography, message } from 'antd';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { parametersApi } from '../api/parameters.api';
import { ParameterControl } from '../components/ParameterControl';
import type {
  ParameterDefinition,
  PlatformSettingsFormValues,
  PlatformValues,
} from '../model/parameters';

import { extractErrorMessage, isForbidden } from '@/shared/lib';
import { DefaultHint } from '@/shared/ui';

const { Text } = Typography;

const EXCLUDED_KEYS = new Set(['memory.long_term_top_k', 'agent.bindings']);

// 敏感参数(apiKey 类)和没有可编辑表单语义的复杂结构永不渲染。
const renderable = (def: ParameterDefinition): boolean =>
  !def.sensitive && !EXCLUDED_KEYS.has(def.key);

// 平台参数按领域分 tab 配置:标签映射中文,未知领域回退原始 category。
const CATEGORY_LABELS: Record<string, string> = {
  agent: 'Agent',
  evaluation: '评测',
  memory: '记忆',
  trace: '追踪',
  mcp: 'MCP',
  rag: 'RAG',
};

const categoryLabel = (category: string): string => CATEGORY_LABELS[category] ?? category;

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
    if (def.visual_hint.control === 'embedding_model') {
      // 嵌入模型未设置 = fail-closed（记忆写入失败并告警），非"使用定义默认"。
      hint = <Text type="secondary">未设置（记忆写入将失败并告警）</Text>;
    } else if (d === undefined || d === null || d === '') {
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
        hint !== null ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            {hint}
          </div>
        ) : undefined
      }
    >
      <ParameterControl def={def} />
    </Form.Item>
  );
};

// PlatformTabPanel 是一个领域子 tab 的表单:只渲染本领域的平台级参数,
// 保存只提交本 tab 的 key(merge 写,不动其他领域)。加载后一次性 setFieldsValue,
// tab 切换不销毁表单,未保存的编辑在切走/切回后保留。
const PlatformTabPanel = ({
  category,
  defs,
  initialValues,
}: {
  category: string;
  defs: ParameterDefinition[];
  initialValues: PlatformValues;
}) => {
  const [form] = Form.useForm<PlatformSettingsFormValues>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const initial: PlatformSettingsFormValues = {};
    for (const def of defs) {
      const v = initialValues[def.key];
      if (v !== undefined) {
        initial[def.key] = v as number | string | boolean;
      }
    }
    form.setFieldsValue(initial);
  }, [form, defs, initialValues]);

  const onFinish = useCallback(
    async (formValues: PlatformSettingsFormValues) => {
      setSaving(true);
      const patch: PlatformValues = {};
      for (const def of defs) {
        const v = formValues[def.key];
        if (v === undefined || v === null) continue;
        // embedding_model 允许显式清空（undefined/空串 → 提交空串）：
        // 回退"未配置"即 fail-closed + 告警；缺失时写空串与未设置语义一致。
        if (def.visual_hint.control === 'embedding_model') {
          patch[def.key] = v === undefined || v === null ? '' : v;
          continue;
        }
        // 模型 key 等于定义默认时跳过提交：空模型由 llmgateway 从模型目录
        // 解析默认（DB 留空），避免把未修改的默认模型名重复写库。
        if (def.visual_hint.control === 'model' && v === def.default) continue;
        patch[def.key] = v;
      }
      try {
        await parametersApi.update(patch);
        message.success({ content: `${categoryLabel(category)}参数已保存`, duration: 2 });
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存平台参数失败'), duration: 3 });
        }
      } finally {
        setSaving(false);
      }
    },
    [category, defs],
  );

  return (
    <Form form={form} layout="vertical" onFinish={onFinish}>
      <Row gutter={[24, 8]}>
        {defs.map((def) => (
          <Col key={def.key} xs={24} md={12}>
            <PlatformFieldItem def={def} />
          </Col>
        ))}
      </Row>
      <Form.Item style={{ marginBottom: 0, marginTop: 16 }}>
        <Button type="primary" htmlType="submit" loading={saving}>
          保存{categoryLabel(category)}参数
        </Button>
      </Form.Item>
    </Form>
  );
};

export const PlatformSettingsPage = () => {
  const [defs, setDefs] = useState<ParameterDefinition[]>([]);
  const [values, setValues] = useState<PlatformValues>({});
  const [loading, setLoading] = useState(true);

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
        setValues(valuesRes);
      } catch (err) {
        if (!cancelled) {
          message.error({ content: extractErrorMessage(err, '加载平台参数失败'), duration: 3 });
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // 只渲染平台级参数;资源级 key 属于资源(Agent)自有配置,平台页不再展示
  // 资源默认值,后端也不再对资源级 key 做平台默认兜底。
  const tabs = useMemo(() => {
    const byCategory = new Map<string, ParameterDefinition[]>();
    for (const def of defs) {
      if (!renderable(def) || def.scope !== 'platform') continue;
      const category = def.category || '其他';
      const group = byCategory.get(category) ?? [];
      group.push(def);
      byCategory.set(category, group);
    }
    return Array.from(byCategory.entries()).map(([category, group]) => ({
      key: category,
      label: categoryLabel(category),
      children: (
        <PlatformTabPanel category={category} defs={group} initialValues={values} />
      ),
    }));
  }, [defs, values]);

  // 加载期间只渲染明确加载态，避免"标题+卡片骨架+保存按钮"的半成品展示页
  // （数据就绪后一次性渲染可编辑配置页）。置于全部 hooks 之后，保持 hook 顺序稳定。
  if (loading) {
    return (
      <div style={{ maxWidth: 960, margin: '0 auto', padding: '24px 16px' }}>
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto', padding: '24px 16px' }}>
      <div style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ marginBottom: 4 }}>
          平台参数
        </Typography.Title>
        <Text type="secondary">
          全局平台参数按领域分页配置，各领域独立保存。0 = 未设置（使用定义默认）。
        </Text>
      </div>
      <Tabs items={tabs} />
    </div>
  );
};
