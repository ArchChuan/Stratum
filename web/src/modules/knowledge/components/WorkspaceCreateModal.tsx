import { Button, Collapse, Form, Input, InputNumber, Modal, Select, type FormInstance } from 'antd';

import {
  CHUNKING_STRATEGY_OPTIONS,
  EMBEDDING_MODEL_OPTIONS,
  KNOWLEDGE_DEFAULT_CHUNK_OVERLAP,
  KNOWLEDGE_DEFAULT_CHUNK_SIZE,
  KNOWLEDGE_DEFAULT_TOP_K,
  KNOWLEDGE_MAX_CHUNK_OVERLAP,
  KNOWLEDGE_MAX_CHUNK_SIZE,
  KNOWLEDGE_MAX_TOP_K,
  KNOWLEDGE_MIN_CHUNK_OVERLAP,
  KNOWLEDGE_MIN_CHUNK_SIZE,
  KNOWLEDGE_MIN_TOP_K,
} from '@/constants';

export interface WorkspaceCreateValues {
  name: string;
  description?: string;
  embedding_model: string;
  chunking_strategy: string;
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

export function WorkspaceCreateModal({
  open,
  loading,
  form,
  onClose,
  onSubmit,
}: WorkspaceCreateModalProps) {
  return (
    <Modal
      className="mobile-overlay"
      title="新建知识库"
      open={open}
      onCancel={() => {
        onClose();
        form.resetFields();
      }}
      footer={null}
      destroyOnHidden
      width={480}
      styles={{ body: { maxHeight: 'min(70vh, 680px)', overflowY: 'auto' } }}
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
        <Form.Item
          label="分块策略"
          name="chunking_strategy"
          initialValue="structure_recursive"
          rules={[{ required: true }]}
          extra="创建后不可修改，影响文档索引结构"
        >
          <Select options={CHUNKING_STRATEGY_OPTIONS} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item label="查询模式" name="query_mode" initialValue="hybrid">
          <Select>
            <Select.Option value="vector">向量检索</Select.Option>
            <Select.Option value="keyword">关键词检索</Select.Option>
            <Select.Option value="hybrid">混合检索（向量 + 关键词）</Select.Option>
          </Select>
        </Form.Item>
        <Form.Item label="Top-K" name="top_k" initialValue={KNOWLEDGE_DEFAULT_TOP_K}>
          <InputNumber
            min={KNOWLEDGE_MIN_TOP_K}
            max={KNOWLEDGE_MAX_TOP_K}
            style={{ width: '100%' }}
          />
        </Form.Item>
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: 'advanced',
              label: '高级设置',
              children: (
                <>
                  <Form.Item
                    label="片段长度"
                    name="chunk_size"
                    initialValue={KNOWLEDGE_DEFAULT_CHUNK_SIZE}
                    extra="越大上下文越完整，但检索颗粒度更粗"
                  >
                    <InputNumber
                      min={KNOWLEDGE_MIN_CHUNK_SIZE}
                      max={KNOWLEDGE_MAX_CHUNK_SIZE}
                      style={{ width: '100%' }}
                    />
                  </Form.Item>
                  <Form.Item
                    label="片段衔接长度"
                    name="chunk_overlap"
                    initialValue={KNOWLEDGE_DEFAULT_CHUNK_OVERLAP}
                    extra="保留片段边界上下文，过大会增加重复内容"
                  >
                    <InputNumber
                      min={KNOWLEDGE_MIN_CHUNK_OVERLAP}
                      max={KNOWLEDGE_MAX_CHUNK_OVERLAP}
                      style={{ width: '100%' }}
                    />
                  </Form.Item>
                </>
              ),
            },
          ]}
        />
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" block loading={loading}>
            创建
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  );
}
