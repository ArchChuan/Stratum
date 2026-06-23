import { DatabaseOutlined } from '@ant-design/icons';
import { Form, Select, Switch } from 'antd';

import { MEMORY_SCOPE_OPTIONS } from '@/constants';
import { SectionHeader } from '@/shared/ui';

export const AgentMemoryConfig = () => {
  const form = Form.useFormInstance();
  const memoryEnabled = Form.useWatch('memoryEnabled', form);

  return (
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
        label="启用记忆"
        name="memoryEnabled"
        valuePropName="checked"
        extra="启用后，Agent 将自动记录和检索对话中的事实与实体"
      >
        <Switch />
      </Form.Item>

      <Form.Item
        label="作用域"
        name="memoryScope"
        rules={[{ required: memoryEnabled, message: '启用记忆时必须选择作用域' }]}
        extra="决定记忆的存储与检索范围"
        style={{ marginBottom: 0 }}
      >
        <Select
          placeholder="选择作用域"
          options={MEMORY_SCOPE_OPTIONS}
          disabled={!memoryEnabled}
        />
      </Form.Item>
    </div>
  );
};
