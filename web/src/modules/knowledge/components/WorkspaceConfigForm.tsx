import { Button, Card, Form, InputNumber, Select, type FormInstance } from 'antd';

const { Option } = Select;

interface ConfigValues {
  query_mode?: string;
  chunk_size?: number;
  chunk_overlap?: number;
  top_k?: number;
  reranking?: string;
  score_threshold?: number;
  rerank_top_k?: number;
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
    <Form className="responsive-inline-form" form={form} layout="inline" onFinish={onSubmit}>
      <Form.Item label="查询模式" name="query_mode">
        <Select style={{ width: '100%', maxWidth: 160 }}>
          <Option value="vector">向量检索</Option>
          <Option value="keyword">关键词检索</Option>
          <Option value="hybrid">混合检索</Option>
        </Select>
      </Form.Item>
      <Form.Item label="分块大小" name="chunk_size" tooltip="创建后不可修改">
        <InputNumber min={64} max={2048} style={{ width: '100%', maxWidth: 100 }} disabled />
      </Form.Item>
      <Form.Item label="分块重叠" name="chunk_overlap" tooltip="创建后不可修改">
        <InputNumber min={0} max={512} style={{ width: '100%', maxWidth: 100 }} disabled />
      </Form.Item>
      <Form.Item label="Top-K" name="top_k">
        <InputNumber min={1} max={20} style={{ width: '100%', maxWidth: 80 }} />
      </Form.Item>
      <Form.Item label="重排策略" name="reranking" tooltip="内置重排按相关度二次排序；外部重排需在模型管理中配置">
        <Select style={{ width: '100%', maxWidth: 140 }} allowClear placeholder="关闭">
          <Option value="">关闭</Option>
          <Option value="builtin-score-v1">内置重排</Option>
        </Select>
      </Form.Item>
      <Form.Item label="分数阈值" name="score_threshold" tooltip="仅保留相似度 ≥ 阈值的检索结果；0 = 不启用（无法回写为 0）">
        <InputNumber min={0} max={1} step={0.05} style={{ width: '100%', maxWidth: 100 }} />
      </Form.Item>
      <Form.Item label="重排 Top-K" name="rerank_top_k" tooltip="外部重排后的最终结果数；0 = 使用 Top-K">
        <InputNumber min={0} max={20} style={{ width: '100%', maxWidth: 100 }} />
      </Form.Item>
      <Form.Item>
        <Button type="primary" htmlType="submit" loading={loading}>
          保存
        </Button>
      </Form.Item>
    </Form>
  </Card>
);
