import { Form, Input, Modal, Select } from 'antd';

import type { CreateAdminTenantInput } from '../../api/tenant.api';

type Props = {
  open: boolean;
  loading: boolean;
  onCancel: () => void;
  onCreate: (input: CreateAdminTenantInput) => Promise<void>;
};

const initialValues: Pick<CreateAdminTenantInput, 'plan' | 'status'> = {
  plan: 'free',
  status: 'active',
};

export const CreateTenantModal = ({ open, loading, onCancel, onCreate }: Props) => {
  const [form] = Form.useForm<CreateAdminTenantInput>();

  const cancel = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Modal
      title="创建租户"
      open={open}
      okText="创建租户"
      cancelText="取消"
      confirmLoading={loading}
      onCancel={cancel}
      onOk={() => void form.validateFields().then(onCreate).catch(() => undefined)}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" initialValues={initialValues} preserve={false}>
        <Form.Item label="租户名称" name="name" rules={[{ required: true, message: '请输入租户名称' }]}>
          <Input autoFocus placeholder="例如：数据平台团队" />
        </Form.Item>
        <Form.Item
          label="Slug"
          name="slug"
          rules={[
            { required: true, message: '请输入 Slug' },
            { pattern: /^[a-z0-9]+(?:-[a-z0-9]+)*$/, message: '仅支持小写字母、数字和连字符' },
          ]}
          extra="用于稳定标识租户，创建后不应随意更改"
        >
          <Input placeholder="data-platform" />
        </Form.Item>
        <Form.Item label="套餐" name="plan" rules={[{ required: true }]}>
          <Select options={[
            { value: 'free', label: '免费版' },
            { value: 'pro', label: '专业版' },
            { value: 'enterprise', label: '企业版' },
          ]} />
        </Form.Item>
        <Form.Item label="初始状态" name="status" rules={[{ required: true }]}>
          <Select options={[
            { value: 'active', label: '启用' },
            { value: 'suspended', label: '禁用' },
          ]} />
        </Form.Item>
      </Form>
    </Modal>
  );
};
