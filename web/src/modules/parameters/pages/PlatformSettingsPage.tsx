import { Button, Col, Form, Row, Skeleton, Tabs, Typography, message } from 'antd';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { parametersApi } from '../api/parameters.api';
import { ParameterControl } from '../components/ParameterControl';
import VersionHistory from '../components/VersionHistory';
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

// buildPatch 从表单值构造平台参数 patch：跳过未设置(null/undefined)、embedding_model
// 显式清空提交空串、模型 key 等于定义默认时跳过（由 llmgateway 从目录解析默认）。
const buildPatch = (defs: ParameterDefinition[], formValues: PlatformSettingsFormValues): PlatformValues => {
  const patch: PlatformValues = {};
  for (const def of defs) {
    const v = formValues[def.key];
    if (v === undefined || v === null) continue;
    if (def.visual_hint.control === 'embedding_model') {
      patch[def.key] = v === undefined || v === null ? '' : v;
      continue;
    }
    if (def.visual_hint.control === 'model' && v === def.default) continue;
    patch[def.key] = v;
  }
  return patch;
};

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

// 版本历史只挂有分组映射的领域（与后端 GroupForKey 四分组一一对应）。
// mcp/rag 等 category 无 platform_config_groups 行，挂了会 404。
const VERSION_GROUPS = new Set(['agent', 'memory', 'evaluation', 'trace']);

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
  groupKey,
  refreshTick,
  onEffectiveChange,
  onDraftSaved,
}: {
  category: string;
  defs: ParameterDefinition[];
  initialValues: PlatformValues;
  groupKey: string | null;
  refreshTick: number;
  onEffectiveChange: (values: PlatformValues) => void;
  // 有版本分组保存草稿后回调：父级递增 versionTick 触发版本历史重拉。
  onDraftSaved?: () => void;
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
      const patch = buildPatch(defs, formValues);
      try {
        if (groupKey) {
          // 有版本分组 → 保存草稿（未生效），发布在版本历史中操作。
          await parametersApi.createDraft(groupKey, patch, '草稿保存');
          message.success({ content: '草稿已保存（未生效）', duration: 2 });
          onDraftSaved?.();
          return;
        }
        // 无版本分组（mcp/rag）→ 立即生效。
        const effective = await parametersApi.update(patch);
        // update 返回发布后生效快照（0/unset 已裁剪），回填表单与版本历史，
        // 让"表单所见 = 版本历史最新 = 运行生效"三处一致。
        if (effective && typeof effective === 'object') {
          onEffectiveChange(effective);
        }
        message.success({ content: `${categoryLabel(category)}参数已保存`, duration: 2 });
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存平台参数失败'), duration: 3 });
        }
      } finally {
        setSaving(false);
      }
    },
    [category, defs, groupKey, onEffectiveChange, onDraftSaved],
  );

  // key→display_name 映射，版本 diff 展开行用中文名而非裸 key。
  const labelMap = useMemo(() => {
    const m: Record<string, string> = {};
    for (const def of defs) m[def.key] = def.display_name || def.key;
    return m;
  }, [defs]);

  return (
    <div>
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
            {groupKey ? '保存草稿' : `保存${categoryLabel(category)}参数`}
          </Button>
        </Form.Item>
      </Form>
      {groupKey ? (
        <VersionHistory
          groupKey={groupKey}
          labelMap={labelMap}
          refreshTick={refreshTick}
          onEffectiveChange={onEffectiveChange}
        />
      ) : null}
    </div>
  );
};

export const PlatformSettingsPage = () => {
  const [defs, setDefs] = useState<ParameterDefinition[]>([]);
  const [values, setValues] = useState<PlatformValues>({});
  const [loading, setLoading] = useState(true);
  // versionTick 在保存/发布/回滚后递增，驱动各 tab 版本历史重拉；
  // groupEffective 保存发布/回滚返回的生效快照，仅刷新对应分组表单。
  const [versionTick, setVersionTick] = useState(0);
  const [groupEffective, setGroupEffective] = useState<Record<string, PlatformValues>>({});

  const handleEffectiveChange = useCallback((category: string, vals: PlatformValues) => {
    setGroupEffective((prev) => ({ ...prev, [category]: vals }));
    setVersionTick((t) => t + 1);
  }, []);

  // 草稿保存只影响版本历史（draft 行出现「发布」），不改变生效值：仅递增
  // versionTick 触发各分组版本历史重拉。
  const handleDraftSaved = useCallback(() => {
    setVersionTick((t) => t + 1);
  }, []);

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
    return Array.from(byCategory.entries()).map(([category, group]) => {
      // 本组当前生效快照：发布/回滚后以返回值覆盖初始值（仅本组），
      // 未变更的分组继续用初始值，避免切走未保存编辑被冲掉。
      const effective = groupEffective[category] ?? values;
      const groupKey = VERSION_GROUPS.has(category) ? category : null;
      return {
        key: category,
        label: categoryLabel(category),
        children: (
          <PlatformTabPanel
            category={category}
            defs={group}
            initialValues={effective}
            groupKey={groupKey}
            refreshTick={versionTick}
            onEffectiveChange={(vals) => handleEffectiveChange(category, vals)}
            onDraftSaved={handleDraftSaved}
          />
        ),
      };
    });
  }, [defs, values, groupEffective, versionTick, handleEffectiveChange, handleDraftSaved]);

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
