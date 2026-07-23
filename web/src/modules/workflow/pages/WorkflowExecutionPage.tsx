import { ArrowLeftOutlined, PlayCircleOutlined } from '@ant-design/icons';
import { Button, Card, Empty, Skeleton, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';

import { WorkflowRunForm } from '../components/WorkflowRunForm';
import { useWorkflowExecution } from '../hooks/useWorkflowExecution';

const { Paragraph, Title } = Typography;

export const WorkflowExecutionPage = () => {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const execution = useWorkflowExecution(id);
  if (execution.loading) return <Skeleton active />;
  if (!execution.version) return <Empty description="这个工作流还没有可运行的版本" />;
  return <section className="workflow-page-shell workflow-execution-page">
    <header className="workflow-execution-header">
      <Button aria-label="返回工作流列表" type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')} />
      <span className="workflow-section-mark"><PlayCircleOutlined /></span>
      <div><Title level={3}>{execution.version.name}</Title><Paragraph>{execution.version.description || '填写本次任务后开始运行。'}</Paragraph></div>
    </header>
    <Card title={`运行版本 ${execution.version.version}`}>
      <WorkflowRunForm schema={execution.version.input_schema} loading={execution.submitting} onSubmit={async (values) => {
        const result = await execution.start(values);
        if (result) navigate(`/workflow-runs/${result.run_id}`);
      }} />
    </Card>
  </section>;
};
