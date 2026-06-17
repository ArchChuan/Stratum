import { Input, Modal, Select, Space } from 'antd';

import type { NewMemoryInput } from '../model/memory';

const { TextArea } = Input;

interface MemoryCreateModalProps {
  open: boolean;
  value: NewMemoryInput;
  onChange: (next: NewMemoryInput) => void;
  onOk: () => void;
  onCancel: () => void;
}

export const MemoryCreateModal = ({
  open,
  value,
  onChange,
  onOk,
  onCancel,
}: MemoryCreateModalProps) => (
  <Modal
    title="添加记忆"
    open={open}
    onOk={onOk}
    onCancel={onCancel}
    okText="添加"
    cancelText="取消"
  >
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Select
        value={value.role}
        onChange={(v) => onChange({ ...value, role: v })}
        style={{ width: '100%' }}
        options={[
          { value: 'user', label: '用户' },
          { value: 'assistant', label: '助手' },
          { value: 'system', label: '系统' },
        ]}
      />
      <TextArea
        rows={4}
        value={value.content}
        onChange={(e) => onChange({ ...value, content: e.target.value })}
        placeholder="记忆内容..."
      />
      <Select
        mode="tags"
        value={value.tags}
        onChange={(v) => onChange({ ...value, tags: v })}
        placeholder="添加标签"
        style={{ width: '100%' }}
      />
    </Space>
  </Modal>
);
