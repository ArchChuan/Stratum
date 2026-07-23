import { Button, Card, Input, Modal, Space, Typography, message } from 'antd';
import { useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowApproval } from '../model/workflow';

const { Paragraph, Text } = Typography;

export const WorkflowApprovalPanel = ({ approval, generation, onChanged }: { approval: WorkflowApproval; generation: number; onChanged: () => void }) => {
  const [comment, setComment] = useState('');
  const decide = (decision: 'approve' | 'reject') => Modal.confirm({
    title: decision === 'approve' ? '批准并继续？' : '拒绝并终止当前步骤？',
    content: approval.request_summary,
    onOk: async () => {
      try {
        await workflowApi.decideWorkflowApproval(approval.id, { run_id: approval.run_id, attempt_id: approval.attempt_id, expected_generation: generation, decision, comment });
        message.success({ content: '操作成功', duration: 2 }); onChanged();
      } catch (error: unknown) { message.error({ content: (error as { response?: { data?: { error?: string } } }).response?.data?.error || '操作失败', duration: 0 }); onChanged(); }
    },
  });
  return <Card title="等待审批"><Paragraph>{approval.reason}</Paragraph><Text type="secondary">风险：{approval.risk}</Text><Input.TextArea aria-label="审批意见" value={comment} onChange={(event) => setComment(event.target.value)} /><Space><Button type="primary" onClick={() => decide('approve')}>批准</Button><Button danger onClick={() => decide('reject')}>拒绝</Button></Space></Card>;
};
