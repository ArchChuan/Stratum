import { Button, Card, Col, Form, Input, InputNumber, Modal, Row, Select, type FormInstance } from 'antd';

import {
  KNOWLEDGE_DEFAULT_CHUNK_OVERLAP,
  KNOWLEDGE_DEFAULT_CHUNK_SIZE,
  KNOWLEDGE_DEFAULT_QUERY_MODE,
  KNOWLEDGE_DEFAULT_TOP_K,
  KNOWLEDGE_MAX_CHUNK_OVERLAP,
  KNOWLEDGE_MAX_CHUNK_SIZE,
  KNOWLEDGE_MAX_RERANK_TOP_K,
  KNOWLEDGE_MAX_SCORE_THRESHOLD,
  KNOWLEDGE_MAX_SCORING_INSTRUCTIONS_RUNES,
  KNOWLEDGE_MAX_TOP_K,
  KNOWLEDGE_MIN_CHUNK_OVERLAP,
  KNOWLEDGE_MIN_CHUNK_SIZE,
  KNOWLEDGE_MIN_RERANK_TOP_K,
  KNOWLEDGE_MIN_SCORE_THRESHOLD,
  KNOWLEDGE_MIN_TOP_K,
} from '@/constants';
import { ModelSelect } from '@/modules/llm/components/ModelSelect';

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
  rerank_scoring_instructions?: string;
  judge_scoring_instructions?: string;
}

interface WorkspaceConfigFormProps {
  form: FormInstance<ConfigValues>;
  loading: boolean;
  onSubmit: (values: ConfigValues) => void;
  /** 撤销未保存的编辑（纯前端回填最近一次生效配置）；未注入则不渲染撤销按钮。 */
  onUndo?: () => void;
}

// unset = undefined/null/''：知识库字段的 0 是显式合法值（score_threshold
// 0=不启用、rerank_top_k 0=跟随 Top-K），不视为未设置。placeholder 只提示
// 不写回，保存/回填逻辑不受影响。
const defaultPlaceholder = (unset: boolean, text: string): string | undefined =>
  unset ? text : undefined;

const cardStyle = { borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 };

export const WorkspaceConfigForm = ({ form, loading, onSubmit, onUndo }: WorkspaceConfigFormProps) => {
  // 撤销未保存的编辑：先确认，再交由页面回填最近一次生效配置。
  const handleUndo = () => {
    if (!onUndo) return;
    Modal.confirm({
      title: '撤销未保存的编辑？',
      content: '表单将重置为最近一次生效的配置。',
      okText: '撤销',
      cancelText: '取消',
      onOk: onUndo,
    });
  };

  const queryMode = Form.useWatch('query_mode', form);
  const chunkSize = Form.useWatch('chunk_size', form);
  const chunkOverlap = Form.useWatch('chunk_overlap', form);
  const topK = Form.useWatch('top_k', form);
  const scoreThreshold = Form.useWatch('score_threshold', form);
  const rerankTopK = Form.useWatch('rerank_top_k', form);
  const reranking = Form.useWatch('reranking', form);

  return (
    <Form
      className="responsive-inline-form"
      form={form}
      layout="vertical"
      size="small"
      // responsive-inline-form 全局 class 是 inline 平铺的 flex；两卡竖排改用
      // block 覆盖，同时保留该 class（responsivePages 断言知识库配置内容参与
      // 响应式布局）。
      style={{ display: 'block' }}
      onFinish={onSubmit}
    >
      <Card title="基础检索" size="small" style={cardStyle}>
        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item label="查询模式" name="query_mode">
              <Select
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
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item
              label="Top-K"
              name="top_k"
              rules={[{
                type: 'number',
                min: KNOWLEDGE_MIN_TOP_K,
                max: KNOWLEDGE_MAX_TOP_K,
                message: `Top-K 需在 ${KNOWLEDGE_MIN_TOP_K}-${KNOWLEDGE_MAX_TOP_K} 之间`,
              }]}
            >
              <InputNumber
                min={KNOWLEDGE_MIN_TOP_K}
                max={KNOWLEDGE_MAX_TOP_K}
                style={{ width: '100%' }}
                placeholder={defaultPlaceholder(topK == null, `默认：${KNOWLEDGE_DEFAULT_TOP_K}`)}
              />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item
              label="分数阈值"
              name="score_threshold"
              rules={[{
                type: 'number',
                min: KNOWLEDGE_MIN_SCORE_THRESHOLD,
                max: KNOWLEDGE_MAX_SCORE_THRESHOLD,
                message: `分数阈值需在 ${KNOWLEDGE_MIN_SCORE_THRESHOLD}-${KNOWLEDGE_MAX_SCORE_THRESHOLD} 之间`,
              }]}
              tooltip="仅保留相似度 ≥ 阈值的检索结果；0 = 不启用（无法回写为 0）"
            >
              <InputNumber
                min={KNOWLEDGE_MIN_SCORE_THRESHOLD}
                max={KNOWLEDGE_MAX_SCORE_THRESHOLD}
                step={0.05}
                style={{ width: '100%' }}
                placeholder={defaultPlaceholder(scoreThreshold == null, '默认：0（不启用）')}
              />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item label="分块大小" name="chunk_size" tooltip="创建后不可修改">
              <InputNumber
                min={KNOWLEDGE_MIN_CHUNK_SIZE}
                max={KNOWLEDGE_MAX_CHUNK_SIZE}
                style={{ width: '100%' }}
                disabled
                placeholder={defaultPlaceholder(chunkSize == null, `默认：${KNOWLEDGE_DEFAULT_CHUNK_SIZE}`)}
              />
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item label="分块重叠" name="chunk_overlap" tooltip="创建后不可修改">
              <InputNumber
                min={KNOWLEDGE_MIN_CHUNK_OVERLAP}
                max={KNOWLEDGE_MAX_CHUNK_OVERLAP}
                style={{ width: '100%' }}
                disabled
                placeholder={defaultPlaceholder(chunkOverlap == null, `默认：${KNOWLEDGE_DEFAULT_CHUNK_OVERLAP}`)}
              />
            </Form.Item>
          </Col>
        </Row>
      </Card>
      <Card title="重排与精排" size="small" style={cardStyle}>
        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item label="重排策略" name="reranking" tooltip="内置重排需在模型管理中配置重排模型；未配置模型时无法保存。外部重排需在模型管理中配置">
              <Select allowClear placeholder="关闭">
                <Option value="">关闭</Option>
                <Option value="builtin-score-v1">内置重排</Option>
              </Select>
            </Form.Item>
          </Col>
          <Col xs={24} sm={8}>
            <Form.Item
              label="重排 Top-K"
              name="rerank_top_k"
              rules={[{
                type: 'number',
                min: KNOWLEDGE_MIN_RERANK_TOP_K,
                max: KNOWLEDGE_MAX_RERANK_TOP_K,
                message: `重排 Top-K 需在 ${KNOWLEDGE_MIN_RERANK_TOP_K}-${KNOWLEDGE_MAX_RERANK_TOP_K} 之间`,
              }]}
              tooltip="外部重排后的最终结果数；0 = 使用 Top-K"
            >
              <InputNumber
                min={KNOWLEDGE_MIN_RERANK_TOP_K}
                max={KNOWLEDGE_MAX_RERANK_TOP_K}
                style={{ width: '100%' }}
                placeholder={defaultPlaceholder(rerankTopK == null, '默认：0（跟随 Top-K）')}
              />
            </Form.Item>
          </Col>
          {reranking === 'builtin-score-v1' && (
            <Col xs={24} sm={8}>
              <Form.Item
                label="重排模型"
                name="rerank_model"
                preserve={false}
                rules={[{ required: true, message: '内置重排必须选择重排模型' }]}
                tooltip="内置重排的 LLM 语义精排模型（chat 目录）；切换重排策略即关闭"
              >
                <ModelSelect placeholder="选择重排模型" allowClear />
              </Form.Item>
            </Col>
          )}
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={8}>
            <Form.Item label="判断模型" name="judge_model" tooltip="证据充分性判断模型（chat 目录）；清空即关闭判断门">
              <ModelSelect placeholder="选择判断模型" allowClear />
            </Form.Item>
          </Col>
        </Row>
        {reranking === 'builtin-score-v1' && (
          <Form.Item
            label="重排评分指令"
            name="rerank_scoring_instructions"
            tooltip="内置重排的相关性打分标准（附加段）；留空使用内置评分标准。JSON 输出结构固定，不可修改"
          >
            <Input.TextArea
              rows={3}
              showCount
              maxLength={KNOWLEDGE_MAX_SCORING_INSTRUCTIONS_RUNES}
              placeholder="留空使用内置评分标准"
            />
          </Form.Item>
        )}
        <Form.Item
          label="证据充分性评分指令"
          name="judge_scoring_instructions"
          tooltip="证据充分性判断的评分标准（附加段，需配置判断模型后生效）；留空使用内置评分标准。JSON 输出结构固定，不可修改"
        >
          <Input.TextArea
            rows={3}
            showCount
            maxLength={KNOWLEDGE_MAX_SCORING_INSTRUCTIONS_RUNES}
            placeholder="留空使用内置评分标准"
          />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" size="small" loading={loading}>
            保存
          </Button>
          {onUndo && (
            <Button onClick={handleUndo} disabled={loading} style={{ marginLeft: 8 }}>
              撤销
            </Button>
          )}
        </Form.Item>
      </Card>
    </Form>
  );
};
