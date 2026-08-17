import { Card, Tag } from 'antd';

import type { TaskSnapshot } from '../model/agent';

interface Props {
  snapshot: TaskSnapshot;
}

/**
 * 跨会话目标进度摘要条：展示 task 的 goal / current_phase / next_action。
 * 仅随 assistant 回复出现（SSE done metadata.stratum_task_snapshot 白名单透出）。
 */
export default function TaskProgressBanner({ snapshot }: Props) {
  const done = snapshot.status === 'completed';
  return (
    <Card size="small" style={{ marginBottom: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Tag color={done ? 'success' : 'processing'}>{done ? '已完成' : '任务推进中'}</Tag>
        <span>{snapshot.goal}</span>
        <span style={{ color: 'rgba(0,0,0,0.45)' }}>{snapshot.currentPhase}</span>
        {!done && snapshot.nextAction ? (
          <span style={{ color: 'rgba(0,0,0,0.45)' }}>下一步：{snapshot.nextAction}</span>
        ) : null}
      </div>
    </Card>
  );
}
