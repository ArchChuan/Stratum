import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Input, Select, Typography } from 'antd';

import { useCreateSkillPage } from '../hooks/useCreateSkillPage';

import { useEditorCandidates } from '@/modules/iam';

const { Title, Text } = Typography;
const { TextArea } = Input;
const { Option } = Select;

export const CreateSkillPage = () => {
  const {
    form,
    loading,
    navigate,
    onFinish,
  } = useCreateSkillPage();
  const { candidates, loading: editorCandidatesLoading } = useEditorCandidates();

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
          label="描述"
          name="description"
          rules={[{ required: true, message: '请输入技能描述' }]}
          extra="一句话说明这个 Skill 的用途"
        >
          <TextArea rows={2} placeholder="例如：判断客户投诉类型并给出处理建议" />
        </Form.Item>

        <Form.Item label="执行指令" name="instructions" rules={[{ required: true, message: '请输入执行指令' }]}>
          <TextArea rows={6} placeholder="描述 Agent 激活此 Skill 后必须遵循的步骤、约束和输出要求" />
        </Form.Item>
        <Form.Item
          label="可编辑人"
          name="editors"
          extra="白名单中的成员可编辑此技能；删除仍仅限创建者或超级管理员"
          style={{ marginBottom: 0 }}
        >
          <Select mode="multiple" placeholder="选择可编辑的租户成员" allowClear loading={editorCandidatesLoading}>
            {candidates.map((member) => (
              <Option key={member.user_id} value={member.user_id}>
                {member.github_login || member.user_id}
              </Option>
            ))}
          </Select>
        </Form.Item>

        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/skills')}>取消</Button>
          <Button
            type="primary"
            htmlType="submit"
            loading={loading}
          >
            创建
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default CreateSkillPage;
