import React, { useState } from 'react';
import { 
  Form, 
  Input, 
  Select, 
  Button, 
  Card, 
  Space, 
  Typography, 
  notification,
  Divider 
} from 'antd';
import { createSkill } from '../services/api';
import { useNavigate } from 'react-router-dom';

const { Title } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const CreateSkillPage = () => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  const onFinish = async (values) => {
    setLoading(true);
    try {
      await createSkill(values);
      notification.success({
        message: '创建成功',
        description: `技能 "${values.name}" 已成功创建`,
      });
      form.resetFields();
      navigate('/skills');
    } catch (error) {
      console.error('Error creating skill:', error);
      notification.error({
        message: '创建失败',
        description: error.message || '创建技能时发生错误',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleCancel = () => {
    navigate('/skills');
  };

  return (
    <div>
      <Title level={2}>创建新技能</Title>
      
      <Card style={{ marginTop: 24 }}>
        <Form
          form={form}
          name="createSkill"
          labelCol={{ span: 4 }}
          wrapperCol={{ span: 14 }}
          layout="horizontal"
          onFinish={onFinish}
          initialValues={{ type: 'code', language: 'python' }}
        >
          <Form.Item
            label="技能名称"
            name="name"
            rules={[{ required: true, message: '请输入技能名称!' }]}
          >
            <Input placeholder="请输入技能名称" />
          </Form.Item>

          <Form.Item
            label="技能描述"
            name="description"
          >
            <TextArea rows={3} placeholder="请输入技能描述" />
          </Form.Item>

          <Form.Item
            label="技能类型"
            name="type"
            rules={[{ required: true, message: '请选择技能类型!' }]}
          >
            <Select placeholder="请选择技能类型">
              <Option value="code">代码技能</Option>
              <Option value="llm">LLM 技能</Option>
              <Option value="builtin">内置技能</Option>
            </Select>
          </Form.Item>

          <Form.Item
            noStyle
            shouldUpdate={(prevValues, curValues) => prevValues.type !== curValues.type}
          >
            {({ getFieldValue }) => {
              const type = getFieldValue('type');
              
              if (type === 'code') {
                return (
                  <>
                    <Form.Item
                      label="编程语言"
                      name="language"
                      rules={[{ required: true, message: '请选择编程语言!' }]}
                    >
                      <Select placeholder="请选择编程语言">
                        <Option value="python">Python</Option>
                        <Option value="javascript">JavaScript</Option>
                        <Option value="go">Go</Option>
                        <Option value="other">其他</Option>
                      </Select>
                    </Form.Item>

                    <Form.Item
                      label="代码内容"
                      name="code"
                      rules={[{ required: true, message: '请输入代码内容!' }]}
                    >
                      <TextArea 
                        rows={10} 
                        placeholder="请输入代码内容" 
                        style={{ fontFamily: 'monospace', fontSize: '14px' }}
                      />
                    </Form.Item>
                    
                    <Divider>示例代码</Divider>
                    <div style={{ padding: '16px', backgroundColor: '#f5f5f5', borderRadius: '4px', marginBottom: '16px' }}>
                      <Typography.Text strong>Python 示例:</Typography.Text>
                      <pre style={{ margin: '8px 0', padding: '8px', backgroundColor: '#fff', borderRadius: '2px' }}>
                        {`def add(a, b):\n    return a + b\n\ndef multiply(a, b):\n    return a * b`}
                      </pre>
                      
                      <Typography.Text strong>JavaScript 示例:</Typography.Text>
                      <pre style={{ margin: '8px 0', padding: '8px', backgroundColor: '#fff', borderRadius: '2px' }}>
                        {`function add(a, b) {\n    return a + b;\n}\n\nfunction multiply(a, b) {\n    return a * b;\n}`}
                      </pre>
                    </div>
                  </>
                );
              }
              
              return null;
            }}
          </Form.Item>

          <Form.Item wrapperCol={{ offset: 4, span: 14 }}>
            <Space size="middle">
              <Button type="primary" htmlType="submit" loading={loading}>
                创建技能
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

export default CreateSkillPage;