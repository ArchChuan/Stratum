import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Input, Typography } from 'antd';

import { useCreateSkillPage } from '../hooks/useCreateSkillPage';

const { Title, Text } = Typography;
const { TextArea } = Input;

export const CreateSkillPage = () => {
  const {
    form,
    loading,
    navigate,
    onFinish,
  } = useCreateSkillPage();

  return (
    <div className="responsive-form-page">
      <div className="responsive-detail-header" style={{ marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            创建技能
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            先定义 Agent 可调用的最小能力包
          </Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
      >
        <Form.Item
          label="名称"
          name="name"
          rules={[{ required: true, message: '请输入技能名称' }]}
          extra="用业务动作命名，例如：投诉分类、订单状态查询"
        >
          <Input placeholder="例如：投诉分类" />
        </Form.Item>

        <Form.Item
          label="能力目标"
          name="goal"
          rules={[{ required: true, message: '请输入能力目标' }]}
          extra="说明这个 Skill 要完成什么业务动作"
        >
          <TextArea rows={3} placeholder="例如：判断客户投诉类型并给出处理建议" />
        </Form.Item>

        <Form.Item
          label="调用时机"
          name="whenToUse"
          rules={[{ required: true, message: '请输入调用时机' }]}
          extra="说明 Agent 在什么情况下应该调用这个 Skill"
        >
          <TextArea rows={3} placeholder="例如：用户表达投诉、退款、物流延迟、商品质量问题时" />
        </Form.Item>

        <Form.Item
          label="样例输入"
          name="sampleInput"
          rules={[{ required: true, message: '请输入样例输入' }]}
          extra="系统会根据样例推断输入结构，并生成第一条测试样例"
        >
          <TextArea rows={3} placeholder="例如：我的快递三天没有更新" />
        </Form.Item>

        <Form.Item
          label="期望输出"
          name="expectedOutput"
          rules={[{ required: true, message: '请输入期望输出' }]}
          extra="描述这个样例下的正确结果"
        >
          <TextArea rows={3} placeholder="例如：物流问题，建议查询物流并安抚用户" />
        </Form.Item>

        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/skills')}>取消</Button>
          <Button
            type="primary"
            htmlType="submit"
            loading={loading}
          >
            创建草稿
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default CreateSkillPage;
