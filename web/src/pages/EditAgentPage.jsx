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
  InputNumber,
  Tag,
  message
} from 'antd';
import { getAgentById, updateAgent, getAllSkills, getAvailableModels } from '../services/api';
import { useNavigate, useParams } from 'react-router-dom';

const { Title } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const FALLBACK_MODELS = ['glm-4', 'glm-4-flash', 'qwen-plus', 'qwen-turbo'];

const EditAgentPage = () => {
  const { id } = useParams();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState([]);
  const [availableModels, setAvailableModels] = useState([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const navigate = useNavigate();

  useEffect(() => {
    loadModels();
    loadSkills();
    loadAgent();
  }, [id]);

  const loadModels = async () => {
    try {
      const res = await getAvailableModels();
      const models = res.data.models?.length > 0 ? res.data.models : FALLBACK_MODELS;
      setAvailableModels(models);
    } catch {
      message.warning('加载模型列表失败，使用默认列表');
      setAvailableModels(FALLBACK_MODELS);
    } finally {
      setModelsLoading(false);
    }
  };

  const loadSkills = async () => {
    try {
      const response = await getAllSkills();
      setSkills(response.data.skills || []);
    } catch (error) {
      message.error(error.response?.data?.error || '加载技能列表失败');
    }
  };

  const loadAgent = async () => {
    try {
      const res = await getAgentById(id);
      const agent = res.data;
      form.setFieldsValue({
        name: agent.name,
        description: agent.description,
        persona: agent.persona,
        systemPrompt: agent.systemPrompt,
        llmModel: agent.llmModel,
        maxIterations: agent.maxIterations,
        allowedSkills: agent.allowedSkills || [],
      });
    } catch {
      message.error('加载代理信息失败');
      navigate('/agents');
    }
  };

  const onFinish = async (values) => {
    setLoading(true);
    try {
      await updateAgent(id, values);
      notification.success({
        message: '保存成功',
        description: `智能代理 "${values.name}" 已成功更新`,
      });
      navigate('/agents');
    } catch (error) {
      if (error.response?.status !== 403) {
        notification.error({
          message: '保存失败',
          description: error.response?.data?.error || error.message || '保存智能代理时发生错误',
        });
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <Title level={2}>编辑智能代理</Title>

      <Card style={{ marginTop: 24 }}>
        <Form
          form={form}
          name="editAgent"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 14 }}
          layout="horizontal"
          onFinish={onFinish}
          initialValues={{
            llmModel: '',
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
              placeholder="描述此代理的角色和行为特征"
            />
          </Form.Item>

          <Form.Item
            label="系统提示词"
            name="systemPrompt"
          >
            <TextArea
              rows={6}
              placeholder="定义代理的行为准则和工作方式"
            />
          </Form.Item>

          <Form.Item
            label="LLM模型"
            name="llmModel"
            rules={[{ required: true, message: '请选择LLM模型!' }]}
          >
            <Select placeholder="请选择LLM模型" loading={modelsLoading}>
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
                保存修改
              </Button>
              <Button onClick={() => navigate('/agents')}>
                取消
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
};

export default EditAgentPage;
