import { SettingOutlined } from '@ant-design/icons';
import { Form, Input, Button } from 'antd';
import { useEffect } from 'react';

import { SectionHeader } from '@/shared/ui';

interface Props {
  initialName: string;
  loading: boolean;
  onSave: (values: { name: string }) => void;
}

export const TenantBasicCard = ({ initialName, loading, onSave }: Props) => {
  const [form] = Form.useForm<{ name: string }>();

  useEffect(() => {
    form.setFieldsValue({ name: initialName });
  }, [form, initialName]);

  return (
    <div
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        height: '100%',
      }}
    >
      <SectionHeader icon={<SettingOutlined />} title="基本信息" subtitle="租户名称等基础配置" />
      <Form form={form} layout="vertical" initialValues={{ name: initialName }} onFinish={onSave}>
        <Form.Item
          label="租户名称"
          name="name"
          rules={[{ required: true, message: '请输入租户名称' }]}
          style={{ marginBottom: 0 }}
        >
          <Input maxLength={64} />
        </Form.Item>
        <div style={{ marginTop: 16 }}>
          <Button type="primary" htmlType="submit" loading={loading}>
            保存
          </Button>
        </div>
      </Form>
    </div>
  );
};
