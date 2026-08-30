import { useNavigate } from 'react-router-dom';

import { WorkflowCatalogHeader } from '../components/WorkflowCatalogHeader';
import { WorkflowCatalogTable } from '../components/WorkflowCatalogTable';
import { useWorkflowCatalog } from '../hooks/useWorkflowCatalog';

import { useTenantRole } from '@/modules/iam';

import '../workflow.css';

export const WorkflowCatalogPage = () => {
  const navigate = useNavigate();
  const { isAdmin } = useTenantRole();
  const { workflows, query, search, page, pageSize, total, loading, deleteWorkflow, setPage, setPageSize } = useWorkflowCatalog();
  const emptyText = query ? '没有找到匹配的工作流' : '工作流还是空的';

  return <section className="workflow-page-shell">
    <WorkflowCatalogHeader query={query} onSearch={search} onCreate={() => navigate('/workflows/new')} />
    <WorkflowCatalogTable
      workflows={workflows}
      loading={loading}
      emptyText={emptyText}
      // 编辑放开给 member（白名单判定在 designer 页），删除保持 admin。
      canEdit
      canManage={isAdmin}
      pagination={{ current: page, pageSize, total, showSizeChanger: true }}
      onPageChange={(nextPage, nextPageSize) => { setPage(nextPage); setPageSize(nextPageSize); }}
      onView={(workflow) => navigate(`/workflows/${workflow.id}`)}
      onRun={(workflow) => navigate(`/workflows/${workflow.id}/run`)}
      onEdit={(workflow) => navigate(`/workflows/${workflow.id}/edit`)}
      onDelete={(workflow) => deleteWorkflow(workflow.id)}
    />
  </section>;
};
