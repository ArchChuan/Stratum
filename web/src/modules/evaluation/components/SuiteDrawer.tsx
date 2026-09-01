import { Alert, Button, Collapse, Drawer, Flex, Modal, Skeleton, Space, Tag, Typography, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type { EvaluationCase, SuiteRevision, SuiteSummary } from '../model/evaluation';

import { EditDraftCaseModal, type EditDraftCaseValues } from './EditDraftCaseModal';
import { GenerateCasesModal, type GenerateCasesValues } from './GenerateCasesModal';
import { displayLabel, drawerWidth, SafeValue, StatusTag } from './evaluationView';

import { extractErrorMessage } from '@/shared/lib';

const modeTag = (mode: string) => <Tag color="blue">{displayLabel(mode)}</Tag>;

// toolSpecSummary 渲染工具序列确定性断言（§6.5）的紧凑摘要：必调用/禁调用/
// 顺序/上限；全空时返回 '—'。
const toolSpecSummary = (spec: NonNullable<EvaluationCase['tool_spec']>) => {
  const parts: string[] = [];
  if (spec.must_call?.length) parts.push(`必调用:${spec.must_call.join('/')}`);
  if (spec.must_not_call?.length) parts.push(`禁调用:${spec.must_not_call.join('/')}`);
  if (spec.order?.length) parts.push(`顺序:${spec.order.join('>')}`);
  if (spec.max_calls) parts.push(`上限:${spec.max_calls}`);
  return parts.join('；') || '—';
};

export const SuiteDrawer = ({ suite, open, onClose, canManage, onChanged, isMobile }: {
  suite: SuiteSummary | null; open: boolean; onClose: () => void; canManage: boolean;
  onChanged: () => void; isMobile?: boolean;
}) => {
  const [draft, setDraft] = useState<SuiteRevision | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [editCase, setEditCase] = useState<EvaluationCase | null>(null);
  const [generateOpen, setGenerateOpen] = useState(false);

  const load = useCallback(async () => {
    if (!suite) return;
    setLoading(true); setError('');
    try { setDraft(await evaluationApi.getSuiteDraft(suite.id)); }
    catch (err) { setError(extractErrorMessage(err) || '加载套件草稿失败'); }
    finally { setLoading(false); }
  }, [suite]);

  useEffect(() => { if (open) { setDraft(null); setEditCase(null); setGenerateOpen(false); void load(); } }, [open, load]);

  const fail = (err: unknown, fallback: string) =>
    message.error({ content: extractErrorMessage(err) || fallback, duration: 3 });

  const saveCase = async (values: EditDraftCaseValues) => {
    if (!suite || !editCase?.id) throw new Error('套件或用例不可用');
    try {
      await evaluationApi.updateDraftCase(suite.id, editCase.id, values);
      await load(); onChanged();
      message.success({ content: '草稿用例已更新', duration: 2 });
    } catch (err) { fail(err, '更新草稿用例失败'); throw err; }
  };

  const generate = async (values: GenerateCasesValues) => {
    if (!suite) throw new Error('套件不可用');
    try {
      const result = await evaluationApi.generateSuiteCases(suite.id, values);
      await load(); onChanged();
      message.success({ content: `已生成 ${result.generated} 个草稿用例（采样 ${result.samples_found}，拒绝 ${result.rejected.length} 个）`, duration: 2 });
    } catch (err) { fail(err, '生成草稿用例失败'); throw err; }
  };

  const publish = () => suite && Modal.confirm({ title: '发布此套件？',
    content: '发布后草稿归档为不可变版本，后续评测运行使用该版本；发布前请审阅全部用例与 AI 判定配置。',
    okText: '发布', cancelText: '取消', onOk: async () => {
      try {
        const published = await evaluationApi.publishSuite(suite.id);
        message.success({ content: `套件已发布 v${published.version_no ?? 1}`, duration: 2 });
        onChanged(); onClose();
      } catch (err) { fail(err, '发布套件失败'); }
    } });

  const provenanceOf = (testCase: EvaluationCase) => {
    const rows: Array<[string, string]> = [];
    if (testCase.source_trace_id) rows.push(['Trace', testCase.source_trace_id]);
    if (testCase.feedback_ref) rows.push(['反馈', testCase.feedback_ref]);
    if (testCase.generate_reason) rows.push(['生成原因', testCase.generate_reason]);
    return rows.length
      ? rows.map(([key, value]) => <div key={key}><Typography.Text type="secondary">{key}：</Typography.Text>
        <Typography.Text code ellipsis style={{ maxWidth: 520 }}>{value}</Typography.Text></div>)
      : null;
  };

  const caseChildren = (testCase: EvaluationCase) => <>
    <Flex gap={8} wrap style={{ marginBottom: 8 }}>
      {modeTag(testCase.assertion_mode)}
      {testCase.enabled ? <Tag color="green">包含在本版本</Tag> : <Tag>已从本版本排除</Tag>}
    </Flex>
    <div><Typography.Text type="secondary">测试输入</Typography.Text><br /><SafeValue value={testCase.input} /></div>
    <div style={{ marginTop: 8 }}><Typography.Text type="secondary">期望输出</Typography.Text><br /><SafeValue value={testCase.expected_output} /></div>
    {testCase.assertion_mode === 'judge' && testCase.judge_spec && <div style={{ marginTop: 8 }}>
      <Typography.Text type="secondary">AI 判定配置</Typography.Text><br />
      <Typography.Text>模型：{testCase.judge_spec.model || '—'}</Typography.Text><br />
      <Typography.Text>评分标准：{testCase.judge_spec.rubric || '—'}</Typography.Text>
    </div>}
    {(testCase.tool_spec || testCase.step_judge?.criteria) && <div style={{ marginTop: 8 }}>
      <Typography.Text type="secondary">过程判定配置</Typography.Text><br />
      {testCase.tool_spec && <Typography.Text>工具断言：{toolSpecSummary(testCase.tool_spec)}</Typography.Text>}
      {testCase.step_judge?.criteria && <>
        {testCase.tool_spec && <br />}
        <Typography.Text>步骤判定：{testCase.step_judge.criteria}</Typography.Text>
      </>}
    </div>}
    {provenanceOf(testCase) && <div style={{ marginTop: 8 }}>
      <Typography.Text type="secondary">生成来源</Typography.Text><br />{provenanceOf(testCase)}
    </div>}
    {canManage && <Button size="small" style={{ marginTop: 12 }} onClick={() => setEditCase(testCase)}>编辑</Button>}
  </>;

  return <Drawer title={suite?.name || '套件'} open={open} onClose={onClose} width={drawerWidth(isMobile)} destroyOnHidden
    extra={canManage && draft ? <Space>
      <Button onClick={() => setGenerateOpen(true)}>生成用例</Button>
      <Button type="primary" onClick={publish}>发布</Button>
    </Space> : null}>
    {suite && <>
      <Flex gap={8} style={{ marginBottom: 8 }}>
        <StatusTag value={suite.status} />
        {draft && <Tag>{displayLabel(draft.resource_kind)}</Tag>}
      </Flex>
      {suite.description && <Typography.Paragraph type="secondary">{suite.description}</Typography.Paragraph>}
      {loading && <Skeleton active />}
      {error && <Alert type="error" showIcon message={error} action={<Button size="small" onClick={() => void load()}>重试</Button>} />}
      {!loading && !error && draft && draft.cases.length === 0
        && <Alert type="info" showIcon message="草稿还没有用例，可生成用例或新建套件时补充。" />}
      {!loading && !error && draft && draft.cases.length > 0 && <Collapse
        defaultActiveKey={draft.cases.map((testCase) => testCase.id || testCase.name)}
        items={draft.cases.map((testCase) => ({
          key: testCase.id || testCase.name, label: <Flex gap={8} align="center">{testCase.name || '未命名'}{modeTag(testCase.assertion_mode)}</Flex>,
          children: caseChildren(testCase),
        }))} />}
    </>}
    <EditDraftCaseModal open={!!editCase} draft={editCase} onClose={() => setEditCase(null)} onSubmit={saveCase} />
    <GenerateCasesModal open={generateOpen} onClose={() => setGenerateOpen(false)} onSubmit={generate} />
  </Drawer>;
};
