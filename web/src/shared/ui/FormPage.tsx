import { Button, Card, Form, Space, Typography } from 'antd';
import type { FormInstance } from 'antd';
import type { ReactNode } from 'react';

const { Title, Text } = Typography;

export interface FormPageProps<V> {
  title: string;
  description?: string;
  form: FormInstance<V>;
  initialValues?: Partial<V>;
  onFinish: (values: V) => void | Promise<void>;
  submitLabel?: string;
  submitting?: boolean;
  onBack?: () => void;
  children: ReactNode;
}

export function FormPage<V>({
  title,
  description,
  form,
  initialValues,
  onFinish,
  submitLabel = '保存',
  submitting,
  onBack,
  children,
}: FormPageProps<V>) {
  return (
    <div style={{ maxWidth: 720 }}>
      <div style={{ marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>
          {title}
        </Title>
        {description && (
          <Text type="secondary" style={{ fontSize: 13 }}>
            {description}
          </Text>
        )}
      </div>
      <Card>
        <Form<V>
          form={form}
          layout="vertical"
          initialValues={initialValues}
          onFinish={onFinish}
        >
          {children}
          <Form.Item style={{ marginBottom: 0, marginTop: 16 }}>
            <Space className="responsive-form-actions">
              <Button type="primary" htmlType="submit" loading={submitting}>
                {submitLabel}
              </Button>
              {onBack && <Button onClick={onBack}>返回</Button>}
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}

export default FormPage;
