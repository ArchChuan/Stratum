import { Badge, Button, Modal, Space } from 'antd';

import type { Agent, AgentExecutionResult } from '../model/agent';

interface AgentResultModalProps {
  agent: Agent | null;
  open: boolean;
  result: AgentExecutionResult | null;
  onClose: () => void;
}

export const AgentResultModal = ({ agent, open, result, onClose }: AgentResultModalProps) => (
  <Modal
    title={
      <Space>
        <Badge status={result?.error ? 'error' : 'success'} />
        {`执行结果 — ${agent?.name || ''}`}
      </Space>
    }
    open={open}
    onCancel={onClose}
    footer={<Button onClick={onClose}>关闭</Button>}
    width={680}
    destroyOnClose
  >
    <pre
      style={{
        background: '#f5f7fa',
        padding: 16,
        borderRadius: 8,
        maxHeight: 400,
        overflow: 'auto',
        whiteSpace: 'pre-wrap',
        wordWrap: 'break-word',
        fontSize: 13,
        lineHeight: 1.6,
      }}
    >
      {JSON.stringify(result, null, 2)}
    </pre>
  </Modal>
);
