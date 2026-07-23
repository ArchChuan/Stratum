import { ArrowLeftOutlined, CheckCircleOutlined, CloudUploadOutlined, SaveOutlined } from '@ant-design/icons';
import { Button, Space, Tag, Typography } from 'antd';

const { Text, Title } = Typography;

export const WorkflowDesignerHeader = ({
  name,
  revision,
  dirty,
  saving,
  validating,
  publishing,
  canValidate,
  canPublish,
  onBack,
  onSave,
  onValidate,
  onPublish,
}: {
  name: string;
  revision?: number;
  dirty: boolean;
  saving: boolean;
  validating: boolean;
  publishing: boolean;
  canValidate: boolean;
  canPublish: boolean;
  onBack: () => void;
  onSave: () => void;
  onValidate: () => void;
  onPublish: () => void;
}) => <header className="workflow-designer-header">
  <Space>
    <Button aria-label="返回工作流列表" type="text" icon={<ArrowLeftOutlined />} onClick={onBack} />
    <div><Title level={3}>{name || '新建工作流'}</Title><Text type="secondary">{revision ? `草稿修订 ${revision}` : '尚未保存'} · {dirty ? '有未保存修改' : '已保存'}</Text></div>
  </Space>
  <Space wrap>
    {canPublish && <Tag color="success">校验通过</Tag>}
    <Button aria-label="保存草稿" icon={<SaveOutlined />} loading={saving} onClick={onSave}>保存草稿</Button>
    <Button aria-label="校验工作流" icon={<CheckCircleOutlined />} loading={validating} disabled={!canValidate} onClick={onValidate}>校验</Button>
    <Button aria-label="发布工作流" type="primary" icon={<CloudUploadOutlined />} loading={publishing} disabled={!canPublish} onClick={onPublish}>发布</Button>
  </Space>
</header>;
