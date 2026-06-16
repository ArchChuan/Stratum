import React from 'react';
import {
  Form, Input, Select, Button, Typography, Tag, InputNumber, Spin, Collapse,
} from 'antd';
import {
  ArrowLeftOutlined, ThunderboltOutlined, CodeOutlined, RobotOutlined, GlobalOutlined,
} from '@ant-design/icons';
import { SKILL_DEFAULT_TEMPERATURE, SKILL_DEFAULT_MAX_TOKENS, SKILL_DEFAULT_TIMEOUT_SEC } from '../constants';
import useEditSkillPage from '../hooks/useEditSkillPage';

const { Title, Text } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const TYPE_META = {
  code: { label: '代码技能', color: '#52c41a', bg: '#f6ffed', icon: <CodeOutlined /> },
  llm: { label: 'LLM 技能', color: '#1677ff', bg: '#e6f4ff', icon: <RobotOutlined /> },
  http: { label: 'HTTP 技能', color: '#fa8c16', bg: '#fff7e6', icon: <GlobalOutlined /> },
};

const SectionHeader = ({ icon, title, subtitle }) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
    <div style={{
      width: 32, height: 32, borderRadius: 8,
      background: '#f0f5ff', display: 'flex', alignItems: 'center', justifyContent: 'center',
    }}>
      {React.cloneElement(icon, { style: { color: '#2f54eb', fontSize: 16 } })}
    </div>
    <div>
      <Text strong style={{ fontSize: 14, display: 'block' }}>{title}</Text>
      {subtitle && <Text type="secondary" style={{ fontSize: 12 }}>{subtitle}</Text>}
    </div>
  </div>
);

const CODE_EXAMPLES = {
  python: `def process(input_data):
    # input_data: dict with task input
    result = input_data.get("query", "")
    return {"output": result.upper()}`,
  javascript: `function process(inputData) {
    // inputData: object with task input
    const result = inputData.query || "";
    return { output: result.toUpperCase() };
}`,
};

const EditSkillPage = () => {
  const {
    form, loading, fetchLoading, availableModels, modelsLoading,
    skillType, language, navigate, onFinish,
  } = useEditSkillPage();

  const selectedMeta = TYPE_META[skillType] || TYPE_META.code;

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">返回</Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>编辑技能</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>修改技能配置</Text>
        </div>
      </div>

      <Spin spinning={fetchLoading}>
        <Form
          form={form}
          layout="vertical"
          onFinish={onFinish}
          initialValues={{ method: 'POST', timeoutSec: SKILL_DEFAULT_TIMEOUT_SEC, temperature: SKILL_DEFAULT_TEMPERATURE, maxTokens: SKILL_DEFAULT_MAX_TOKENS }}
        >
          {/* 基本信息 */}
          <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
            <SectionHeader icon={<ThunderboltOutlined />} title="基本信息" />
            <Form.Item label="技能名称" name="name" rules={[{ required: true, message: '请输入技能名称' }]}>
              <Input placeholder="例如：数据处理器" />
            </Form.Item>
            <Form.Item label="描述" name="description">
              <TextArea rows={2} placeholder="描述此技能的功能" />
            </Form.Item>
            <Form.Item label="技能类型" name="type" style={{ marginBottom: 0 }}>
              <Select disabled>
                {Object.entries(TYPE_META).map(([val, meta]) => (
                  <Option key={val} value={val}>
                    <Tag style={{ margin: '0 6px 0 0', border: 'none', fontSize: 11, background: meta.bg, color: meta.color }}>
                      {val}
                    </Tag>
                    {meta.label}
                  </Option>
                ))}
              </Select>
            </Form.Item>
          </div>

          {/* 代码配置 */}
          {skillType === 'code' && (
            <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
              <SectionHeader icon={<CodeOutlined />} title="代码" subtitle="技能的具体执行逻辑" />
              <Form.Item label="编程语言" name="language" rules={[{ required: true }]}>
                <Select>
                  <Option value="python">Python</Option>
                  <Option value="javascript">JavaScript</Option>
                </Select>
              </Form.Item>
              <Form.Item label="代码" name="code" rules={[{ required: true, message: '请输入代码' }]}>
                <TextArea rows={10} placeholder="输入技能代码..." style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
              {CODE_EXAMPLES[language] && (
                <div style={{ background: '#f6ffed', border: '1px solid #b7eb8f', borderRadius: 8, padding: '12px 16px' }}>
                  <Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
                    {language} 入口函数示例（process 函数将被调用）
                  </Text>
                  <pre style={{ margin: 0, fontSize: 12, color: '#389e0d', background: 'transparent' }}>
                    {CODE_EXAMPLES[language]}
                  </pre>
                </div>
              )}
            </div>
          )}

          {/* LLM 配置 */}
          {skillType === 'llm' && (
            <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
              <SectionHeader icon={<RobotOutlined />} title="LLM 配置" subtitle="设置模型和提示词" />
              <Form.Item label="系统提示词" name="systemPrompt" rules={[{ required: true, message: '请输入系统提示词' }]}>
                <TextArea rows={5} placeholder="你是一个专业的助手，负责..." style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
              <Form.Item label="模型" name="model" rules={[{ required: true, message: '请选择模型' }]}>
                <Select placeholder="选择推理模型" loading={modelsLoading}>
                  {availableModels.map((m) => <Option key={m} value={m}>{m}</Option>)}
                </Select>
              </Form.Item>
              <Collapse
                ghost
                size="small"
                defaultActiveKey={[]}
                items={[{
                  key: 'advanced',
                  label: <Text type="secondary" style={{ fontSize: 13 }}>高级参数</Text>,
                  children: (
                    <div style={{ display: 'flex', gap: 16 }}>
                      <Form.Item label="Temperature" name="temperature" style={{ flex: 1 }}>
                        <InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} />
                      </Form.Item>
                      <Form.Item label="Max Tokens" name="maxTokens" style={{ flex: 1, marginBottom: 0 }}>
                        <InputNumber min={1} max={32000} step={256} style={{ width: '100%' }} />
                      </Form.Item>
                    </div>
                  ),
                }]}
              />
            </div>
          )}

          {/* HTTP 配置 */}
          {skillType === 'http' && (
            <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
              <SectionHeader icon={<GlobalOutlined />} title="HTTP 配置" subtitle="调用外部 API 接口" />
              <Form.Item label="请求 URL" name="url" rules={[{ required: true, message: '请输入 URL' }, { type: 'url', message: '请输入合法 URL' }]}>
                <Input placeholder="https://api.example.com/endpoint" />
              </Form.Item>
              <div style={{ display: 'flex', gap: 16 }}>
                <Form.Item label="请求方法" name="method" style={{ width: 140 }}>
                  <Select>
                    <Option value="GET">GET</Option>
                    <Option value="POST">POST</Option>
                    <Option value="PUT">PUT</Option>
                    <Option value="PATCH">PATCH</Option>
                    <Option value="DELETE">DELETE</Option>
                  </Select>
                </Form.Item>
                <Form.Item label="超时（秒）" name="timeoutSec" style={{ flex: 1 }}>
                  <InputNumber min={1} max={300} style={{ width: '100%' }} />
                </Form.Item>
              </div>
              <Form.Item
                label="请求头（JSON）"
                name="headersJson"
                rules={[{
                  validator: (_, v) => {
                    if (!v) return Promise.resolve();
                    try { JSON.parse(v); return Promise.resolve(); }
                    catch { return Promise.reject(new Error('必须是合法 JSON')); }
                  }
                }]}
              >
                <TextArea rows={3} placeholder='{"Authorization": "Bearer xxx", "X-Api-Key": "yyy"}' style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
              <Form.Item label="请求体模板（Go text/template）" name="bodyTemplate">
                <TextArea rows={5} placeholder={'{"query": "{{.query}}", "limit": {{.limit}}}'} style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
            </div>
          )}

          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button onClick={() => navigate('/skills')}>取消</Button>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              style={{ background: selectedMeta.color, borderColor: selectedMeta.color }}
            >
              保存
            </Button>
          </div>
        </Form>
      </Spin>
    </div>
  );
};

export default EditSkillPage;
