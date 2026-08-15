import { Descriptions, Drawer, Spin, Tag, Typography } from 'antd';

import type { ResourceChangeAudit } from '../model/audit';
import { OPERATION_LABELS } from '../model/audit';

import { RESOURCE_KIND_OPTIONS } from '@/constants';

interface AuditEventDrawerProps {
  event: ResourceChangeAudit | null;
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
    <Drawer title="审计详情" open={open} width={640} onClose={onClose}>
      <Spin spinning={loading}>
        {event ? (
          <div>
            <Descriptions size="small" column={1} bordered>
              <Descriptions.Item label="时间">{new Date(event.created_at).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="操作者">
                {event.actor_name}
                <Typography.Text type="secondary" style={{ marginLeft: 8 }}>
                  ({event.actor_id})
                </Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="资源类型">
                {RESOURCE_KIND_OPTIONS.find((o) => o.value === event.resource_kind)?.label || event.resource_kind}
              </Descriptions.Item>
              <Descriptions.Item label="资源 ID">{event.resource_id}</Descriptions.Item>
              <Descriptions.Item label="操作">
                <Tag color="blue">{OPERATION_LABELS[event.operation] || event.operation}</Tag>
              </Descriptions.Item>
            </Descriptions>

            <div style={{ marginTop: 16 }}>
              <Typography.Title level={5}>变更投影</Typography.Title>
              <DiffBlock title="变更前" value={event.before} />
              <DiffBlock title="变更后" value={event.after} />
            </div>
          </div>
        ) : (
          <Typography.Text type="secondary">未找到审计记录</Typography.Text>
        )}
      </Spin>
    </Drawer>
  );
};

export default AuditEventDrawer;
