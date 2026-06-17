import { PlayCircleOutlined } from '@ant-design/icons';
import { Input, Modal, Space, Typography } from 'antd';

import type { Agent } from '../model/agent';

const { Text } = Typography;
const { TextArea } = Input;

interface AgentTaskModalProps {
  agent: Agent | null;
  open: boolean;
  loading: boolean;
  taskQuery: string;
  taskContext: string;
  onQueryChange: (v: string) => void;
  onContextChange: (v: string) => void;
  onSubmit: () => void;
  onCancel: () => void;
}

export const AgentTaskModal = ({
  agent,
  open,
  loading,
  taskQuery,
  taskContext,
  onQueryChange,
  onContextChange,
  onSubmit,
  onCancel,
}: AgentTaskModalProps) => (
  <Modal
    title={
      <Space>
        <PlayCircleOutlined />
        {`执行 ${agent?.name || ''}`}
      </Space>
    }
    open={open}
    onOk={onSubmit}
    onCancel={onCancel}
    okText="执行"
    cancelText="取消"
    confirmLoading={loading}
    width={560}
    destroyOnClose
  >
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      <div>
        <div style={{ marginBottom: 6, fontWeight: 500 }}>
          任务内容 <span style={{ color: '#ff4d4f' }}>*</span>
        </div>
        <TextArea
          rows={4}
          value={taskQuery}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder="输入你希望 Agent 执行的任务..."
          autoFocus
        />
      </div>
      <div>
        <div style={{ marginBottom: 6, fontWeight: 500 }}>
          上下文{' '}
          <Text type="secondary" style={{ fontWeight: 400, fontSize: 12 }}>
            （JSON，可选）
          </Text>
        </div>
        <TextArea
          rows={3}
          value={taskContext}
          onChange={(e) => onContextChange(e.target.value)}
          placeholder='{"key": "value"}'
          style={{ fontFamily: 'monospace', fontSize: 13 }}
        />
      </div>
    </Space>
  </Modal>
);
