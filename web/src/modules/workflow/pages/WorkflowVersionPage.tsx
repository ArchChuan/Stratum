import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Card, Descriptions, Empty, message, Skeleton, Tag, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { workflowApi } from '../api/workflow.api';
import { WorkflowReadonlyCanvas } from '../components/WorkflowReadonlyCanvas';
import type { WorkflowVersion } from '../model/workflow';

const { Paragraph, Title } = Typography;
interface RequestError { response?: { data?: { error?: string } } }

export const WorkflowVersionPage = () => {
  const { id = '', versionId = '' } = useParams();
  const navigate = useNavigate();
  const [version, setVersion] = useState<WorkflowVersion | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    workflowApi.getWorkflowVersion(id, versionId).then((next) => {
      if (!cancelled) setVersion(next);
    }).catch((error: unknown) => {
      if (!cancelled) message.error({ content: (error as RequestError).response?.data?.error || '操作失败', duration: 0 });
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [id, versionId]);

  if (loading) return <Skeleton active />;
  if (!version) return <Empty description="没有找到这个工作流版本" />;

  return <section className="workflow-page-shell workflow-version-page">
    <header className="workflow-version-header">
      <Button aria-label="返回工作流列表" type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')} />
      <div><Title level={3}>{version.name}</Title><Paragraph>{version.description || '暂无说明'}</Paragraph></div>
      <Tag color="geekblue">版本 {version.version}</Tag>
    </header>
    <WorkflowReadonlyCanvas spec={version.spec} />
    <Card title="运行输入" className="workflow-version-inputs">
      <Descriptions column={1} size="small">
        <Descriptions.Item label="任务名称">{version.input_schema.task_label}</Descriptions.Item>
        {version.input_schema.fields.map((field) => <Descriptions.Item key={field.key} label={field.label}>
          <Tag>{field.type}</Tag>{field.required ? '必填' : '选填'}
        </Descriptions.Item>)}
      </Descriptions>
    </Card>
  </section>;
};
