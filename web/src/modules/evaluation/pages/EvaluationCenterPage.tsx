import { PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Drawer, Empty, Flex, Select, Skeleton, Space, Table, Tabs, Typography, message } from 'antd';
import { useEffect, useMemo, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import { CandidateDrawer } from '../components/CandidateDrawer';
import { EvaluationOverview } from '../components/EvaluationOverview';
import { ExperimentDrawer } from '../components/ExperimentDrawer';
import { ResourceTable } from '../components/ResourceTable';
import { RunDrawer } from '../components/RunDrawer';
import { TimelineDrawer } from '../components/TimelineDrawer';
import { StatusTag, displayLabel, drawerWidth } from '../components/evaluationView';
import { useEvaluationCenter } from '../hooks/useEvaluationCenter';
import type { CandidateSummary, ExperimentSummary, ResourceKind, ResourceSummary, RunSummary, TimelineEvent } from '../model/evaluation';

import { useResponsive } from '@/shared/hooks';

const resourceOptions = ['skill', 'agent', 'mcp', 'knowledge'].map((value) => ({ value, label: displayLabel(value) }));
const statusOptions = ['active', 'proposed', 'running', 'succeeded', 'failed', 'paused'].map((value) => ({ value, label: displayLabel(value) }));
const command = (version: number, reason: string) => ({ reason, expected_state_version: version,
  idempotency_key: crypto.randomUUID() });

export const EvaluationCenterPage = () => {
  const { isMobile } = useResponsive();
  const [kind, setKind] = useState<ResourceKind | undefined>();
  const [status, setStatus] = useState<string | undefined>();
  const center = useEvaluationCenter({ resource_kind: kind, status });
  const [resourceId, setResourceId] = useState('');
  const [runId, setRunId] = useState('');
  const [candidateId, setCandidateId] = useState('');
  const [experimentId, setExperimentId] = useState('');
  const [timeline, setTimeline] = useState<TimelineEvent[]>([]);
  const [timelineOpen, setTimelineOpen] = useState(false);
  const [timelineLoading, setTimelineLoading] = useState(false);
  const resource = useMemo(() => center.resources.items.find((item) => item.id === resourceId) || null,
    [center.resources.items, resourceId]);
  const run = center.runs.items.find((item) => item.id === runId) || null;
  const candidate = center.candidates.items.find((item) => item.id === candidateId) || null;
  const experiment = center.experiments.items.find((item) => item.id === experimentId) || null;

  useEffect(() => { if (resourceId && !resource) setResourceId(''); }, [resource, resourceId]);
  const openTimeline = async (value: ResourceSummary) => {
    setTimelineOpen(true); setTimelineLoading(true);
    try { setTimeline((await evaluationApi.getTimeline(value.resource_kind, value.resource_id)).items); }
    catch { message.error({ content: '加载资源时间线失败', duration: 0 }); }
    finally { setTimelineLoading(false); }
  };
  const decide = async (action: () => Promise<unknown>, success: string) => {
    try { await action(); message.success({ content: success, duration: 2 }); }
    catch (error) { message.error({ content: error instanceof Error ? error.message : '操作失败', duration: 0 }); }
  };

  if (center.loading && !center.overview) return <Skeleton active />;
  return <div>
    <Flex justify="space-between" align="end" gap={16} wrap style={{ marginBottom: 12 }}>
      <div><Typography.Title level={4} style={{ margin: 0 }}>评测与进化中心</Typography.Title>
        <Typography.Text type="secondary">在同一记录簿中审阅版本证据与演进决定</Typography.Text></div>
      <Space wrap>
        <Select aria-label="资源类型" allowClear placeholder="资源类型" style={{ width: 132 }} options={resourceOptions}
          value={kind} onChange={setKind} />
        <Select aria-label="资源状态" allowClear placeholder="资源状态" style={{ width: 132 }} options={statusOptions}
          value={status} onChange={setStatus} />
        {center.canManageEvaluation && <Button type="primary" icon={<PlusOutlined />}
          onClick={() => message.info('请从目标资源详情中创建评测')}>新建评测</Button>}
      </Space>
    </Flex>
    <EvaluationOverview overview={center.overview} />
    {center.error && <Alert type="error" showIcon message={center.error} action={<Button onClick={center.reload}>重试</Button>} />}
    <ResourceTable resources={center.resources.items} loading={center.loading} filtered={!!kind || !!status} onOpen={(row) => setResourceId(row.id)} />
    <Tabs style={{ marginTop: 16 }} items={[
      { key: 'runs', label: `运行记录 ${center.runs.items.length}`, children: <CompactList rows={center.runs.items}
        empty="运行记录还是空的" onOpen={(row) => setRunId(row.id)} /> },
      { key: 'candidates', label: `候选版本 ${center.candidates.items.length}`, children: <CompactList rows={center.candidates.items}
        empty="候选版本还是空的" onOpen={(row) => setCandidateId(row.id)} /> },
      { key: 'experiments', label: `金丝雀实验 ${center.experiments.items.length}`, children: <CompactList rows={center.experiments.items}
        empty="金丝雀实验还是空的" onOpen={(row) => setExperimentId(row.id)} /> },
    ]} />
    <Drawer title="资源详情" open={!!resource} onClose={() => setResourceId('')} width={drawerWidth(isMobile)} destroyOnHidden>
      {resource && <><Typography.Title level={5}>观测事实</Typography.Title>
        <Typography.Paragraph>资源：{resource.resource_id}</Typography.Paragraph>
        <Typography.Paragraph>稳定版本：{resource.stable_revision_id || '尚未建立'}</Typography.Paragraph>
        <StatusTag value={resource.status} /><Typography.Title level={5}>系统建议</Typography.Title>
        <Alert type="info" message="结合运行、候选与实验记录审阅此资源，不展示原始提示词或载荷。" />
        <Button style={{ marginTop: 16 }} onClick={() => void openTimeline(resource)}>查看时间线</Button></>}
    </Drawer>
    <RunDrawer run={run} open={!!run} onClose={() => setRunId('')} isMobile={isMobile} />
    <CandidateDrawer candidate={candidate} open={!!candidate} onClose={() => setCandidateId('')}
      canManage={center.canManageEvaluation} isMobile={isMobile} onReject={(value) => void decide(
        () => center.rejectCandidate(value.id, command(value.state_version, '管理员拒绝候选版本')), '候选版本已拒绝')} />
    <ExperimentDrawer experiment={experiment} open={!!experiment} onClose={() => setExperimentId('')}
      canManage={center.canManageEvaluation} isMobile={isMobile}
      onPause={(value) => void decide(() => center.pauseExperiment(value.id, command(value.state_version, '管理员暂停实验')), '实验已暂停')}
      onPromote={(value) => void decide(() => center.promoteExperiment(value.id, command(value.state_version, '管理员晋级实验')), '实验已晋级')}
      onRollback={(value) => void decide(() => center.rollbackExperiment(value.id, command(value.state_version, '管理员回滚实验')), '实验已回滚')} />
    <TimelineDrawer events={timeline} open={timelineOpen} loading={timelineLoading} isMobile={isMobile}
      onClose={() => setTimelineOpen(false)} />
  </div>;
};

const CompactList = <T extends RunSummary | CandidateSummary | ExperimentSummary>({ rows, empty, onOpen }: {
  rows: T[]; empty: string; onOpen: (row: T) => void;
}) => <Table<T> size="small" rowKey="id" dataSource={rows} pagination={false}
  locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={empty} /> }} columns={[
    { title: '记录', dataIndex: 'id', ellipsis: true },
    { title: '资源', dataIndex: 'resource_id', ellipsis: true },
    { title: '状态', dataIndex: 'status', width: 120, render: (value: string) => <StatusTag value={value} /> },
    { title: '操作', width: 80, render: (_, row) => <Button type="link" size="small" onClick={() => onOpen(row)}>详情</Button> },
  ]} />;
