import { Button, Card, Input, Modal, Space, Typography, message } from 'antd';
import { useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowEffectIntent } from '../model/workflow';

const { Paragraph } = Typography;

export const WorkflowManualInterventionPanel = ({ intent, generation, onChanged }: { intent: WorkflowEffectIntent; generation: number; onChanged: () => void }) => {
  const [summary, setSummary] = useState('');
  const resolve = (action: 'mark_succeeded' | 'retry' | 'terminate') => Modal.confirm({
    title: '确认人工处置？', content: '该工具调用结果未知，请根据外部系统实际状态谨慎选择。',
    onOk: async () => {
      try { await workflowApi.resolveWorkflowManualIntervention(intent.run_id, intent.id, { expected_generation: generation, action, output_summary: summary }); message.success({ content: '操作成功', duration: 2 }); onChanged(); }
      catch (error: unknown) { message.error({ content: (error as { response?: { data?: { error?: string } } }).response?.data?.error || '操作失败', duration: 0 }); onChanged(); }
    },
  });
  return <Card title="需要人工处置"><Paragraph>{intent.reason}</Paragraph><Input.TextArea aria-label="处置结果摘要" value={summary} onChange={(event) => setSummary(event.target.value)} /><Space wrap><Button onClick={() => resolve('mark_succeeded')}>标记成功</Button><Button onClick={() => resolve('retry')}>重试</Button><Button danger onClick={() => resolve('terminate')}>终止运行</Button></Space></Card>;
};
