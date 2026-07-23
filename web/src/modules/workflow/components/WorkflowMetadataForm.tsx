import { Form, Input } from 'antd';

export const WorkflowMetadataForm = () => <>
  <Form.Item label="工作流名称" name="name" rules={[{ required: true, message: '请输入工作流名称' }]}>
    <Input placeholder="例如：客户研究与审批" maxLength={80} />
  </Form.Item>
  <Form.Item label="工作流说明" name="description" extra="告诉使用者这个工作流会完成什么。">
    <Input.TextArea placeholder="说明适用场景、产出和注意事项" rows={3} maxLength={500} />
  </Form.Item>
</>;
