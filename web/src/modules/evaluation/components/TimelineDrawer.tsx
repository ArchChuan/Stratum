import { Drawer, Empty, Timeline, Typography } from 'antd';

import type { TimelineEvent } from '../model/evaluation';

import { drawerWidth, StatusTag } from './evaluationView';

export const TimelineDrawer = ({ events, open, onClose, loading, isMobile }: {
  events: TimelineEvent[]; open: boolean; onClose: () => void; loading?: boolean; isMobile?: boolean;
}) => <Drawer title="资源时间线" open={open} onClose={onClose} width={drawerWidth(isMobile)} loading={loading} destroyOnHidden>
  {events.length ? <Timeline className="evaluation-timeline-rail" items={events.map((event) => ({
    children: <><Typography.Text strong>{event.summary}</Typography.Text><br /><StatusTag value={event.status} />
      <Typography.Text type="secondary"> {new Date(event.created_at).toLocaleString('zh-CN')}</Typography.Text></>,
  }))} /> : <Empty description="时间线还是空的" />}
</Drawer>;
