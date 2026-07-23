import { Collapse, Descriptions, Empty, Tag, Typography } from 'antd';

import type { WorkflowRunState } from '../model/run-state';

const { Paragraph, Text } = Typography;

export const WorkflowRunInspector = ({ state, nodeId }: { state: WorkflowRunState; nodeId?: string }) => {
  if (!nodeId) return <Empty description="选择一个步骤查看运行详情" />;
  const attempts = state.attempts.filter((attempt) => attempt.node_id === nodeId);
  return <aside aria-label="运行步骤详情" className="workflow-run-inspector">
    <Descriptions column={1} size="small" title="步骤详情">
      <Descriptions.Item label="节点标识">{nodeId}</Descriptions.Item>
      <Descriptions.Item label="实时输出"><Paragraph copyable>{state.outputByNode[nodeId] || '暂无输出'}</Paragraph></Descriptions.Item>
    </Descriptions>
    <Collapse items={attempts.map((attempt) => ({ key: attempt.id, label: `第 ${attempt.attempt_no} 次尝试 · ${attempt.status}`, children: <><Text>{attempt.output_summary || '暂无摘要'}</Text>{attempt.error_message && <Paragraph type="danger">{attempt.error_message}</Paragraph>}</> }))} />
    {(state.toolStepsByNode[nodeId] || []).map((step, index) => <Tag key={`${String(step.tool)}-${index}`}>{String(step.tool || '工具')} · {String(step.latency_ms || 0)}ms</Tag>)}
  </aside>;
};
