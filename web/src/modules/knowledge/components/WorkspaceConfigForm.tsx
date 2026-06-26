import { Button, Card, Form, InputNumber, Select, type FormInstance } from 'antd';

const { Option } = Select;

interface ConfigValues {
  query_mode?: string;
  chunk_size?: number;
  chunk_overlap?: number;
  top_k?: number;
}

interface WorkspaceConfigFormProps {
  form: FormInstance<ConfigValues>;
  loading: boolean;
  onSubmit: (values: ConfigValues) => void;
}

export const WorkspaceConfigForm = ({ form, loading, onSubmit }: WorkspaceConfigFormProps) => (
  <Card
    title="配置管理"
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
  >
    <Form form={form} layout="inline" onFinish={onSubmit}>
      <Form.Item label="查询模式" name="query_mode">
        <Select style={{ width: 160 }}>
          <Option value="vector">向量检索</Option>
          <Option value="keyword">关键词检索</Option>
          <Option value="hybrid">混合检索</Option>
        </Select>
      </Form.Item>
      <Form.Item label="分块大小" name="chunk_size" tooltip="创建后不可修改">
        <InputNumber min={64} max={2048} style={{ width: 100 }} disabled />
      </Form.Item>
      <Form.Item label="分块重叠" name="chunk_overlap" tooltip="创建后不可修改">
        <InputNumber min={0} max={512} style={{ width: 100 }} disabled />
      </Form.Item>
      <Form.Item label="Top-K" name="top_k">
        <InputNumber min={1} max={20} style={{ width: 80 }} />
      </Form.Item>
      <Form.Item>
        <Button type="primary" htmlType="submit" loading={loading}>
          保存
        </Button>
      </Form.Item>
    </Form>
  </Card>
);
