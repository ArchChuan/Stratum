import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Card, Empty, List, Skeleton, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { WorkflowApprovalPanel } from '../components/WorkflowApprovalPanel';
import { WorkflowManualInterventionPanel } from '../components/WorkflowManualInterventionPanel';
import { WorkflowRunActions } from '../components/WorkflowRunActions';
import { WorkflowRunCanvas } from '../components/WorkflowRunCanvas';
import { WorkflowRunInspector } from '../components/WorkflowRunInspector';
import { WorkflowRunProgress } from '../components/WorkflowRunProgress';
import { useWorkflowRunStream } from '../hooks/useWorkflowRunStream';
import { selectFailedNode, selectPendingApproval } from '../model/run-state';

import { useResponsive } from '@/shared/hooks';

const { Paragraph, Title } = Typography;

export const WorkflowRunDetailPage = () => {
  const { runId = '' } = useParams();
  const navigate = useNavigate();
  const { isMobile } = useResponsive();
  const [refreshKey, setRefreshKey] = useState(0);
  const state = useWorkflowRunStream(runId, refreshKey);
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  useEffect(() => { const failed = state && selectFailedNode(state); if (failed) setSelectedNodeId(failed.node_id); }, [state]);
  if (!state) return <Skeleton active />;
  const approval = selectPendingApproval(state);
  const manual = state.effectIntents.find((intent) => intent.status === 'unknown');
  const refresh = () => setRefreshKey((value) => value + 1);
  return <section className="workflow-page-shell"><header className="workflow-run-detail-header"><Button aria-label="返回运行中心" type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflow-runs')} /><div><Title level={3}>运行详情</Title><Paragraph>{state.run.id}</Paragraph></div><WorkflowRunActions state={state} onChanged={refresh} /></header><WorkflowRunProgress state={state} />
    {isMobile ? <List dataSource={state.run.snapshot.nodes} renderItem={(node) => { const attempt = [...state.attempts].reverse().find((item) => item.node_id === node.id); return <List.Item onClick={() => setSelectedNodeId(node.id)}><List.Item.Meta title={node.name || node.type} description={attempt?.status || 'pending'} /></List.Item>; }} /> : <WorkflowRunCanvas state={state} selectedNodeId={selectedNodeId} onSelectNode={setSelectedNodeId} />}
    <WorkflowRunInspector state={state} nodeId={selectedNodeId} />
    {state.run.output && <Card title="最终结果"><Paragraph copyable>{state.run.output}</Paragraph></Card>}
    {approval && <WorkflowApprovalPanel approval={approval} generation={state.run.generation} onChanged={refresh} />}
    {manual && <WorkflowManualInterventionPanel intent={manual} generation={state.run.generation} onChanged={refresh} />}
    {!state.run.snapshot.nodes.length && <Empty description="这个运行没有步骤" />}
  </section>;
};
