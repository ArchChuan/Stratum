import { Button, Col, Form, Input, Modal, Row, Select, type FormInstance } from 'antd';

import { EMBEDDING_MODEL_OPTIONS } from '@/constants';

const { Option } = Select;

export interface WorkspaceCreateValues {
  name: string;
  description?: string;
  embedding_model: string;
  chunk_size?: number;
  chunk_overlap?: number;
  query_mode?: string;
  top_k?: number;
}

interface WorkspaceCreateModalProps {
  open: boolean;
  loading: boolean;
  form: FormInstance<WorkspaceCreateValues>;
  onClose: () => void;
  onSubmit: (values: WorkspaceCreateValues) => void;
}

export const WorkspaceCreateModal = ({
  open,
  loading,
  form,
  onClose,
  onSubmit,
}: WorkspaceCreateModalProps) => (
  <Modal
    title="新建知识库"
    open={open}
    onCancel={() => {
      onClose();
      form.resetFields();
    }}
    footer={null}
    destroyOnHidden
    width={480}
  >
    <Form form={form} layout="vertical" onFinish={onSubmit}>
      <Form.Item
        label="名称"
        name="name"
        rules={[{ required: true, message: '请输入知识库名称' }]}
      >
        <Input placeholder="仅支持字母、数字、连字符" />
      </Form.Item>
      <Form.Item
        label="描述"
        name="description"
        rules={[{ required: true, message: '请输入知识库描述' }]}
        extra="此描述会直接展示给 AI 模型，请准确描述本知识库的内容范围，以便模型判断是否需要搜索"
      >
        <Input.TextArea
          rows={3}
          placeholder="例：包含产品手册、版本说明与常见问题，适用于产品功能咨询"
        />
      </Form.Item>
      <Form.Item
        label="嵌入模型"
        name="embedding_model"
        initialValue="text-embedding-v3"
        rules={[{ required: true }]}
      >
        <Select options={EMBEDDING_MODEL_OPTIONS} style={{ width: '100%' }} />
      </Form.Item>
      <Form.Item label="查询模式" name="query_mode" initialValue="vector">
        <Select>
          <Option value="vector">向量检索</Option>
          <Option value="keyword">关键词检索</Option>
          <Option value="hybrid">混合检索（向量 + 关键词）</Option>
        </Select>
      </Form.Item>
      <Row gutter={12}>
        <Col span={8}>
          <Form.Item label="分块大小" name="chunk_size" initialValue={512}>
            <Input type="number" min={64} max={2048} />
          </Form.Item>
        </Col>
        <Col span={8}>
          <Form.Item label="分块重叠" name="chunk_overlap" initialValue={64}>
            <Input type="number" min={0} max={512} />
          </Form.Item>
        </Col>
        <Col span={8}>
          <Form.Item label="Top-K" name="top_k" initialValue={5}>
            <Input type="number" min={1} max={20} />
          </Form.Item>
        </Col>
      </Row>
      <Form.Item style={{ marginBottom: 0 }}>
        <Button type="primary" htmlType="submit" block loading={loading}>
          创建
        </Button>
      </Form.Item>
    </Form>
  </Modal>
);
