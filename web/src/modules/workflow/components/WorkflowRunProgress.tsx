import { Progress, Space, Tag, Typography } from 'antd';

import { selectCompletedCount, type WorkflowRunState } from '../model/run-state';

const { Text } = Typography;
const labels: Record<string, string> = { queued: '排队中', running: '运行中', completed: '已完成', failed: '失败', paused: '已暂停', pause_requested: '暂停中', cancel_requested: '取消中', canceled: '已取消', manual_intervention: '等待人工处理' };

export const WorkflowRunProgress = ({ state }: { state: WorkflowRunState }) => {
  const completed = selectCompletedCount(state);
  const total = state.run.snapshot.nodes.length;
  return <Space direction="vertical" style={{ width: '100%' }}>
    <Space><Tag>{labels[state.run.status] || state.run.status}</Tag><Text type="secondary">事件连接：{state.connection === 'connected' ? '已连接' : state.connection === 'reconnecting' ? '正在重连' : '未连接'}</Text></Space>
    <Progress percent={total ? Math.round((completed / total) * 100) : 0} format={() => `${completed}/${total}`} />
  </Space>;
};
