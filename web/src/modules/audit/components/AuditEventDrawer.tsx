import { Descriptions, Drawer, Skeleton, Tag, Typography } from 'antd';

import type { AuditEvent } from '../model/audit';
import { OUTCOME_COLORS, OUTCOME_LABELS, RISK_LEVEL_COLORS, RISK_LEVEL_LABELS } from '../model/audit';

interface AuditEventDrawerProps {
  event: AuditEvent | null;
  loading: boolean;
  open: boolean;
  onClose: () => void;
}

const formatJSON = (value: unknown): string => JSON.stringify(value, null, 2);

const DiffBlock = ({ title, value }: { title: string; value?: unknown }) => {
  if (value === undefined || value === null) return null;
  return (
    <div style={{ marginBottom: 12 }}>
      <Typography.Text strong>{title}</Typography.Text>
      <pre
        style={{
          background: '#fafafa',
          border: '1px solid #f0f0f0',
          borderRadius: 6,
          padding: 8,
          fontSize: 12,
          maxHeight: 320,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {formatJSON(value)}
      </pre>
    </div>
  );
};

export const AuditEventDrawer = ({ event, loading, open, onClose }: AuditEventDrawerProps) => {
  return (
    <Drawer title="审计事件详情" open={open} width={640} onClose={onClose}>
      {loading ? (
        <Skeleton active paragraph={{ rows: 8 }} />
      ) : event ? (
        <div>
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label="事件 ID">{event.id}</Descriptions.Item>
            <Descriptions.Item label="租户">{event.tenant_id}</Descriptions.Item>
            <Descriptions.Item label="操作者">
              {event.actor.actor_id}
              <Typography.Text type="secondary" style={{ marginLeft: 8 }}>
                ({event.actor.actor_type})
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="操作">{event.action}</Descriptions.Item>
            <Descriptions.Item label="资源类型">{event.resource_type}</Descriptions.Item>
            <Descriptions.Item label="资源 ID">{event.resource_id}</Descriptions.Item>
            <Descriptions.Item label="风险">
              <Tag color={RISK_LEVEL_COLORS[event.risk_level]}>{RISK_LEVEL_LABELS[event.risk_level] || event.risk_level}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="结果">
              <Tag color={OUTCOME_COLORS[event.outcome]}>{OUTCOME_LABELS[event.outcome] || event.outcome}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="请求 ID">{event.request_id}</Descriptions.Item>
            <Descriptions.Item label="Trace ID">{event.trace_id}</Descriptions.Item>
            <Descriptions.Item label="发生时间">{new Date(event.occurred_at).toLocaleString()}</Descriptions.Item>
          </Descriptions>

          <div style={{ marginTop: 16 }}>
            <Typography.Title level={5}>变更快照</Typography.Title>
            <DiffBlock title="变更前" value={event.before} />
            <DiffBlock title="变更后" value={event.after} />
          </div>
        </div>
      ) : (
        <Typography.Text type="secondary">未找到审计事件</Typography.Text>
      )}
    </Drawer>
  );
};

export default AuditEventDrawer;
