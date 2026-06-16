import React from 'react';
import {
  Form, Input, Select, Button, Space, Typography, InputNumber, Tag, Skeleton,
} from 'antd';
import {
  ArrowLeftOutlined, RobotOutlined, ThunderboltOutlined, SettingOutlined,
} from '@ant-design/icons';
import useEditAgentPage from '../hooks/useEditAgentPage';
import { CHAT_MODEL_OPTIONS } from '../constants';

const { Title, Text } = Typography;
const { TextArea } = Input;

const AGENT_TYPES = [
  { value: 'react',        label: 'ReAct（工具调用 + 推理）',  disabled: false },
  { value: 'cot',          label: 'CoT（思维链推理）',          disabled: true },
  { value: 'planning',     label: 'Planning（规划分解）',       disabled: true },
  { value: 'tool_calling', label: 'Tool Calling（纯工具调用）', disabled: true },
  { value: 'rag',          label: 'RAG（检索增强生成）',        disabled: true },
  { value: 'swarm',        label: 'Swarm（多 Agent 协作）',     disabled: true },
];

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

const EditAgentPage = () => {
  const {
    form, loading, pageLoading,
    skills, mcpServers, workspaces,
    navigate, onFinish,
  } = useEditAgentPage();

  if (pageLoading) {
    return (
      <div style={{ maxWidth: 720, margin: '0 auto' }}>
        <Skeleton active paragraph={{ rows: 1 }} style={{ marginBottom: 24 }} />
        <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
          <Skeleton active paragraph={{ rows: 3 }} />
        </div>
        <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24 }}>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/agents')} type="text">返回</Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>编辑 Agent</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>修改 Agent 配置</Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
        initialValues={{ maxIterations: 25, maxContextTokens: 8000, allowedSkills: [] }}
      >
        <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
          <SectionHeader icon={<RobotOutlined />} title="基本信息" subtitle="Agent 的名称和对外描述" />
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入 Agent 名称' }]}>
            <Input placeholder="例如：数据分析助手" />
          </Form.Item>
          <Form.Item label="类型" name="type" rules={[{ required: true, message: '请选择 Agent 类型' }]}>
            <Select>
              {AGENT_TYPES.map(t => (
                <Option key={t.value} value={t.value} disabled={t.disabled}>
                  {t.label}{t.disabled ? ' （暂未开放）' : ''}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item label="描述" name="description" style={{ marginBottom: 0 }}>
            <TextArea rows={2} placeholder="简述 Agent 的用途" />
          </Form.Item>
        </div>

        <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
          <SectionHeader icon={<ThunderboltOutlined />} title="角色与提示词" subtitle="定义 Agent 的行为特征" />
          <Form.Item label="角色设定" name="persona">
            <TextArea rows={3} placeholder="例如：你是一个专业的数据分析师..." />
          </Form.Item>
          <Form.Item label="系统提示词" name="systemPrompt" style={{ marginBottom: 0 }}>
            <TextArea rows={5} placeholder="定义 Agent 的行为准则、可用工具和响应格式..." />
          </Form.Item>
        </div>

        <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 }}>
          <SectionHeader icon={<SettingOutlined />} title="模型与参数" subtitle="选择推理模型和执行配置" />
          <Form.Item label="LLM 模型" name="llmModel" rules={[{ required: true, message: '请选择模型' }]}>
            <Select placeholder="选择推理模型" options={CHAT_MODEL_OPTIONS} />
          </Form.Item>
          <Form.Item label="最大迭代次数" name="maxIterations" rules={[{ required: true }]}
            tooltip="每次 LLM 推理和工具调用各计 1 步。例如设为 25 则允许约 12 轮「思考→使用工具」循环"
            extra="推荐值：简单对话 10，多步工具调用 20-30，复杂任务 40-50">
            <InputNumber min={1} max={50} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label="最大上下文 Token" name="maxContextTokens" rules={[{ required: true }]}
            tooltip="每次 LLM 调用时，发送消息的估算 Token 上限（CJK 字符按 1/3 估算）"
            extra="推荐值：轻量对话 4000，标准 8000，长文档处理 32000-128000">
            <InputNumber min={1000} max={128000} step={1000} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label="允许使用的技能" name="allowedSkills" style={{ marginBottom: 16 }}
            extra="工具型技能（代码执行、API 调用等）扩展 Agent 的行动能力">
            <Select mode="multiple" placeholder="选择 Agent 可调用的工具技能">
              {skills.map(s => (
                <Option key={s.id} value={s.id}>
                  <Tag style={{ margin: '0 6px 0 0', border: 'none', fontSize: 11 }}
                    color={s.type === 'code' ? 'green' : s.type === 'llm' ? 'orange' : 'default'}>{s.type}</Tag>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item label="挂载 MCP 服务" name="mcpServerIds" style={{ marginBottom: 16 }}
            extra="提供符合 Model Context Protocol 协议的结构化工具">
            <Select mode="multiple" placeholder="选择要挂载的 MCP 服务器">
              {mcpServers.map(s => (
                <Option key={s.id} value={s.id}>{s.name || s.id}</Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item label="挂载知识库" name="knowledgeWorkspaceIds" style={{ marginBottom: 0 }}
            extra="Agent 执行时可自动检索已挂载知识库中的文档（RAG 增强）">
            <Select mode="multiple" placeholder="选择知识库工作区">
              {workspaces.map(w => (
                <Option key={w.id} value={w.id}>{w.name}</Option>
              ))}
            </Select>
          </Form.Item>
        </div>

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/agents')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={loading}>保存修改</Button>
        </div>
      </Form>
    </div>
  );
};

export default EditAgentPage;
