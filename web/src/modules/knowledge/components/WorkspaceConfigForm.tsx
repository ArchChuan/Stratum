import { Button, Card, Form, InputNumber, Select, type FormInstance } from 'antd';

import {
  KNOWLEDGE_DEFAULT_CHUNK_OVERLAP,
  KNOWLEDGE_DEFAULT_CHUNK_SIZE,
  KNOWLEDGE_DEFAULT_QUERY_MODE,
  KNOWLEDGE_DEFAULT_TOP_K,
} from '@/constants';

const { Option } = Select;

// 查询模式值 → 可读 label（默认值提示展示 label，不暴露原始值）。
const QUERY_MODE_LABELS: Record<string, string> = {
  vector: '向量检索',
  keyword: '关键词检索',
  hybrid: '混合检索',
};

interface ConfigValues {
  query_mode?: string;
  chunk_size?: number;
  chunk_overlap?: number;
  top_k?: number;
  reranking?: string;
  rerank_model?: string;
  judge_model?: string;
  score_threshold?: number;
  rerank_top_k?: number;
}

interface WorkspaceConfigFormProps {
  form: FormInstance<ConfigValues>;
  loading: boolean;
  chatModels: string[];
  onSubmit: (values: ConfigValues) => void;
}

// unset = undefined/null/''：知识库字段的 0 是显式合法值（score_threshold
// 0=不启用、rerank_top_k 0=跟随 Top-K），不视为未设置。placeholder 只提示
// 不写回，保存/回填逻辑不受影响。
const defaultPlaceholder = (unset: boolean, text: string): string | undefined =>
  unset ? text : undefined;

export const WorkspaceConfigForm = ({ form, loading, chatModels, onSubmit }: WorkspaceConfigFormProps) => {
  const queryMode = Form.useWatch('query_mode', form);
  const chunkSize = Form.useWatch('chunk_size', form);
  const chunkOverlap = Form.useWatch('chunk_overlap', form);
  const topK = Form.useWatch('top_k', form);
  const scoreThreshold = Form.useWatch('score_threshold', form);
  const rerankTopK = Form.useWatch('rerank_top_k', form);
  const reranking = Form.useWatch('reranking', form);

  return (
    <Card
      title="配置管理"
      style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
    >
      <Form className="responsive-inline-form" form={form} layout="inline" onFinish={onSubmit}>
        <Form.Item label="查询模式" name="query_mode">
          <Select
            style={{ width: '100%', maxWidth: 160 }}
            placeholder={defaultPlaceholder(
              queryMode == null || queryMode === '',
              `默认：${QUERY_MODE_LABELS[KNOWLEDGE_DEFAULT_QUERY_MODE] ?? KNOWLEDGE_DEFAULT_QUERY_MODE}`,
            )}
          >
            <Option value="vector">向量检索</Option>
            <Option value="keyword">关键词检索</Option>
            <Option value="hybrid">混合检索</Option>
          </Select>
        </Form.Item>
        <Form.Item label="分块大小" name="chunk_size" tooltip="创建后不可修改">
          <InputNumber
            min={64}
            max={2048}
            style={{ width: '100%', maxWidth: 100 }}
            disabled
            placeholder={defaultPlaceholder(chunkSize == null, `默认：${KNOWLEDGE_DEFAULT_CHUNK_SIZE}`)}
          />
        </Form.Item>
        <Form.Item label="分块重叠" name="chunk_overlap" tooltip="创建后不可修改">
          <InputNumber
            min={0}
            max={512}
            style={{ width: '100%', maxWidth: 100 }}
            disabled
            placeholder={defaultPlaceholder(chunkOverlap == null, `默认：${KNOWLEDGE_DEFAULT_CHUNK_OVERLAP}`)}
          />
        </Form.Item>
        <Form.Item label="Top-K" name="top_k">
          <InputNumber
            min={1}
            max={20}
            style={{ width: '100%', maxWidth: 80 }}
            placeholder={defaultPlaceholder(topK == null, `默认：${KNOWLEDGE_DEFAULT_TOP_K}`)}
          />
        </Form.Item>
        <Form.Item label="重排策略" name="reranking" tooltip="内置重排需在模型管理中配置重排模型；未配置模型时无法保存。外部重排需在模型管理中配置">
          <Select style={{ width: '100%', maxWidth: 140 }} allowClear placeholder="关闭">
            <Option value="">关闭</Option>
            <Option value="builtin-score-v1">内置重排</Option>
          </Select>
        </Form.Item>
        <Form.Item label="分数阈值" name="score_threshold" tooltip="仅保留相似度 ≥ 阈值的检索结果；0 = 不启用（无法回写为 0）">
          <InputNumber
            min={0}
            max={1}
            step={0.05}
            style={{ width: '100%', maxWidth: 120 }}
            placeholder={defaultPlaceholder(scoreThreshold == null, '默认：0（不启用）')}
          />
        </Form.Item>
        <Form.Item label="重排 Top-K" name="rerank_top_k" tooltip="外部重排后的最终结果数；0 = 使用 Top-K">
          <InputNumber
            min={0}
            max={20}
            style={{ width: '100%', maxWidth: 120 }}
            placeholder={defaultPlaceholder(rerankTopK == null, '默认：0（跟随 Top-K）')}
          />
        </Form.Item>
        {reranking === 'builtin-score-v1' && (
          <Form.Item
            label="重排模型"
            name="rerank_model"
            preserve={false}
            rules={[{ required: true, message: '内置重排必须选择重排模型' }]}
            tooltip="内置重排的 LLM 语义精排模型（chat 目录）；切换重排策略即关闭"
          >
            <Select
              style={{ width: '100%', maxWidth: 160 }}
              placeholder="选择重排模型"
              allowClear
              options={chatModels.map((m) => ({ label: m, value: m }))}
            />
          </Form.Item>
        )}
        <Form.Item label="判断模型" name="judge_model" tooltip="证据充分性判断模型（chat 目录）；清空即关闭判断门">
          <Select
            style={{ width: '100%', maxWidth: 160 }}
            placeholder="选择判断模型"
            allowClear
            options={chatModels.map((m) => ({ label: m, value: m }))}
          />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" loading={loading}>
            保存
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
