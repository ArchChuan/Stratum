import { RobotOutlined, SettingOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Collapse, Form, Input, InputNumber, Select, Tag, Typography } from 'antd';

import { AgentMemoryConfig } from './AgentMemoryConfig';

import { CHAT_MODEL_OPTIONS } from '@/constants';
import type { Workspace } from '@/modules/knowledge';
import type { MCPServer } from '@/modules/mcp';
import type { Skill } from '@/modules/skill';
import { SectionHeader } from '@/shared/ui';

const { Text } = Typography;
const { TextArea } = Input;
const { Option } = Select;

interface AgentFormSectionsProps {
  skills: Skill[];
  mcpServers: MCPServer[];
  workspaces: Workspace[];
}

export const AgentFormSections = ({
  skills,
  mcpServers,
  workspaces,
}: AgentFormSectionsProps) => (
  <>
    <Form.Item name="type" hidden>
      <Input />
    </Form.Item>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<RobotOutlined />}
        title="基本信息"
        subtitle="Agent 的名称和对外描述"
      />
      <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入 Agent 名称' }]}>
        <Input placeholder="例如：数据分析助手" />
      </Form.Item>
      <Form.Item label="描述" name="description" style={{ marginBottom: 0 }}>
        <TextArea rows={2} placeholder="简述 Agent 的用途" />
      </Form.Item>
    </div>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<ThunderboltOutlined />}
        title="提示词"
        subtitle="定义 Agent 的角色和行为"
      />
      <Form.Item
        label="系统提示词"
        name="systemPrompt"
        style={{ marginBottom: 0 }}
        extra="在这里定义角色、能力范围、行为准则和响应格式"
      >
        <TextArea
          rows={8}
          placeholder={
            '你是一个专业的数据分析师，擅长从数据中提取洞察。\n\n行为准则：\n- 回答基于事实，不做无依据推断\n- 复杂问题先拆解再逐步回答\n\n响应格式：使用 Markdown 格式。'
          }
        />
      </Form.Item>
    </div>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<SettingOutlined />}
        title="模型与工具"
        subtitle="选择模型并挂载工具和知识"
      />
      <Form.Item label="LLM 模型" name="llmModel" rules={[{ required: true, message: '请选择模型' }]}>
        <Select placeholder="选择推理模型" options={CHAT_MODEL_OPTIONS} />
      </Form.Item>
      <Form.Item
        label="技能"
        name="allowedSkills"
        style={{ marginBottom: 16 }}
        extra="代码执行、API 调用等工具型技能"
      >
        <Select mode="multiple" placeholder="选择 Agent 可调用的技能">
          {skills.map((s) => (
            <Option key={s.id} value={s.id}>
              <Tag
                style={{ margin: '0 6px 0 0', border: 'none', fontSize: 11 }}
                color={s.type === 'code' ? 'green' : s.type === 'llm' ? 'orange' : 'default'}
              >
                {s.type}
              </Tag>
              {s.name}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Form.Item
        label="MCP 服务"
        name="mcpServerIds"
        style={{ marginBottom: 16 }}
        extra="符合 Model Context Protocol 协议的结构化工具"
      >
        <Select mode="multiple" placeholder="选择 MCP 服务器">
          {mcpServers.map((s) => (
            <Option key={s.id} value={s.id}>
              {s.name || s.id}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Form.Item label="知识库" name="knowledgeWorkspaceIds" extra="执行时自动检索相关文档">
        <Select mode="multiple" placeholder="选择知识库">
          {workspaces.map((w) => (
            <Option key={w.id || w.name} value={w.id || w.name}>
              {w.name}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Collapse
        ghost
        size="small"
        defaultActiveKey={['advanced']}
        items={[
          {
            key: 'advanced',
            label: (
              <Text type="secondary" style={{ fontSize: 13 }}>
                高级设置
              </Text>
            ),
            children: (
              <>
                <Form.Item
                  label="最大迭代次数"
                  name="maxIterations"
                  rules={[{ required: true, message: '请输入最大迭代次数' }, { type: 'number', min: 1, message: '最小值为 1' }]}
                  extra="推荐值：简单对话 10，多步工具调用 20-30，复杂任务 40-50"
                >
                  <InputNumber min={1} max={50} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item
                  label="最大上下文 Token"
                  name="maxContextTokens"
                  rules={[{ required: true, message: '请输入最大上下文 Token' }, { type: 'number', min: 1000, message: '最小值为 1000' }]}
                  extra="推荐值：轻量对话 4000，标准 8000，长文档处理 32000-128000"
                  style={{ marginBottom: 0 }}
                >
                  <InputNumber min={1000} max={128000} step={1000} style={{ width: '100%' }} />
                </Form.Item>
              </>
            ),
          },
        ]}
      />
    </div>

    <AgentMemoryConfig />
  </>
);
