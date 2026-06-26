import { DatabaseOutlined } from '@ant-design/icons';
import { Form, Select } from 'antd';

import { MEMORY_SCOPE_OPTIONS } from '@/constants';
import { SectionHeader } from '@/shared/ui';

export const AgentMemoryConfig = () => (
  <div
    style={{
      background: '#fff',
      borderRadius: 12,
      border: '1px solid #f0f0f0',
      padding: 24,
      marginBottom: 16,
    }}
  >
    <SectionHeader
      icon={<DatabaseOutlined />}
      title="记忆配置"
      subtitle="自动记忆对话上下文"
    />
    <Form.Item
      label="作用域"
      name="memoryScope"
      rules={[{ required: true, message: '请选择作用域' }]}
      extra="决定记忆的存储与检索范围"
      style={{ marginBottom: 0 }}
    >
      <Select placeholder="选择作用域" options={MEMORY_SCOPE_OPTIONS} />
    </Form.Item>
  </div>
);
