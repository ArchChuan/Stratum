import React, { useState, useEffect } from 'react';
import { 
  Form, 
  Input, 
  Select, 
  Button, 
  Card, 
  Space, 
  Typography, 
  notification,
  Divider,
  InputNumber,
  Tag,
  Row,
  Col
} from 'antd';
import { createAgent, getAllSkills } from '../services/api';
import { useNavigate } from 'react-router-dom';

const { Title } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const CreateAgentPage = () => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState([]);
  const [availableModels] = useState([
    'gpt-4', 'gpt-3.5-turbo', 'claude-3-opus', 'claude-3-sonnet', 
    'gemini-pro', 'llama-2', 'mistral-large'
  ]);
  const navigate = useNavigate();

  useEffect(() => {
    loadSkills();
  }, []);

  const loadSkills = async () => {
    try {
      const response = await getAllSkills();
      setSkills(response.data);
    } catch (error) {
      console.error('Error loading skills:', error);
      notification.error({
        message: '加载技能失败',
        description: error.message,
      });
    }
  };

  const onFinish = async (values) => {
    setLoading(true);
    try {
      await createAgent(values);
      notification.success({
        message: '创建成功',
        description: `智能代理 "${values.name}" 已成功创建`,
      });
      form.resetFields();
      navigate('/agents');
    } catch (error) {
      console.error('Error creating agent:', error);
      notification.error({
        message: '创建失败',
        description: error.message || '创建智能代理时发生错误',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleCancel = () => {
    navigate('/agents');
  };

  return (
    <div>
      <Title level={2}>创建智能代理</Title>
      
      <Card style={{ marginTop: 24 }}>
        <Form
          form={form}
          name="createAgent"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 14 }}
          layout="horizontal"
          onFinish={onFinish}
          initialValues={{ 
            llmModel: 'gpt-4', 
            maxIterations: 5,
            allowedSkills: []
          }}
        >
          <Form.Item
            label="代理名称"
            name="name"
            rules={[{ required: true, message: '请输入代理名称!' }]}
          >
            <Input placeholder="请输入代理名称" />
          </Form.Item>

          <Form.Item
            label="描述"
            name="description"
          >
            <TextArea rows={3} placeholder="请输入代理描述" />
          </Form.Item>

          <Form.Item
            label="角色设定"
            name="persona"
          >
            <TextArea 
              rows={4} 
              placeholder="描述此代理的角色和行为特征，例如：'你是一个专业的数据分析助手，专注于从数据中提取有价值的商业洞察'" 
            />
          </Form.Item>

          <Form.Item
            label="系统提示词"
            name="systemPrompt"
          >
            <TextArea 
              rows={6} 
              placeholder="定义代理的行为准则和工作方式，例如：'你是一个AI助手，帮助用户完成任务。你可以使用以下技能：数据处理、文本分析、计算等。'" 
            />
          </Form.Item>

          <Form.Item
            label="LLM模型"
            name="llmModel"
            rules={[{ required: true, message: '请选择LLM模型!' }]}
          >
            <Select placeholder="请选择LLM模型">
              {availableModels.map(model => (
                <Option key={model} value={model}>{model}</Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item
            label="最大迭代次数"
            name="maxIterations"
            rules={[{ required: true, message: '请输入最大迭代次数!' }]}
          >
            <InputNumber 
              min={1} 
              max={20} 
              placeholder="代理执行任务的最大迭代次数"
              style={{ width: '100%' }}
            />
          </Form.Item>

          <Form.Item
            label="允许使用的技能"
            name="allowedSkills"
          >
            <Select 
              mode="multiple" 
              placeholder="选择此代理可以使用的技能"
              loading={skills.length === 0}
            >
              {skills.map(skill => (
                <Option key={skill.id} value={skill.id}>
                  <Tag color={skill.type === 'code' ? 'green' : skill.type === 'llm' ? 'orange' : 'default'}>
                    {skill.type}
                  </Tag>
                  {skill.name}
                </Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item wrapperCol={{ offset: 6, span: 14 }}>
            <Space size="middle">
              <Button type="primary" htmlType="submit" loading={loading}>
                创建代理
              </Button>
              <Button onClick={handleCancel}>
                取消
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
};

export default CreateAgentPage;