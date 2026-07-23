import { HistoryOutlined } from '@ant-design/icons';
import { Typography } from 'antd';
import { useNavigate } from 'react-router-dom';

import { WorkflowRunFilters } from '../components/WorkflowRunFilters';
import { WorkflowRunsTable } from '../components/WorkflowRunsTable';
import { useWorkflowRuns } from '../hooks/useWorkflowRuns';

import { useTenantRole } from '@/modules/iam';

const { Paragraph, Title } = Typography;

export const WorkflowRunsPage = () => {
  const navigate = useNavigate();
  const { isAdmin } = useTenantRole();
  const runs = useWorkflowRuns();
  const hasFilters = Boolean(runs.definitionId || runs.status);
  return <section className="workflow-page-shell">
    <header className="workflow-runs-header"><span className="workflow-section-mark"><HistoryOutlined /></span><div><Title level={3}>{isAdmin ? '运行中心' : '我的运行'}</Title><Paragraph>{isAdmin ? '查看租户内全部工作流运行并处理异常。' : '查看你发起的工作流及其进度。'}</Paragraph></div></header>
    <WorkflowRunFilters isAdmin={isAdmin} definitionId={runs.definitionId} status={runs.status} onDefinitionChange={runs.setDefinitionId} onStatusChange={runs.setStatus} />
    <WorkflowRunsTable runs={runs.runs} loading={runs.loading} emptyText={hasFilters ? '没有找到匹配的运行' : '运行记录还是空的'} pagination={{ current: runs.page, pageSize: runs.pageSize, total: runs.total, showSizeChanger: true }} onPageChange={(page, pageSize) => { runs.setPage(page); runs.setPageSize(pageSize); }} onOpen={(run) => navigate(`/workflow-runs/${run.id}`)} />
  </section>;
};
