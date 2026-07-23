import { CheckCircleOutlined, InfoCircleOutlined } from '@ant-design/icons';
import { Alert } from 'antd';

export const WorkflowValidationPanel = ({ validated }: { validated: boolean }) => validated
  ? <Alert type="success" showIcon icon={<CheckCircleOutlined />} message="当前修订已通过校验，可以发布。" />
  : <Alert type="info" showIcon icon={<InfoCircleOutlined />} message="保存草稿后执行校验，校验通过后才能发布。" />;
