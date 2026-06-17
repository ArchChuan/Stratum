import { Card, Skeleton } from 'antd';
import type { ReactNode } from 'react';

interface StatCardProps {
  loading: boolean;
  title: string;
  value: number;
  icon: ReactNode;
  color: string;
  bg: string;
}

export const StatCard = ({ loading, title, value, icon, color, bg }: StatCardProps) => (
  <Card
    style={{ borderRadius: 12, border: 'none', background: bg, overflow: 'hidden' }}
    styles={{ body: { padding: '20px 24px' } }}
  >
    {loading ? (
      <Skeleton active paragraph={false} title={{ width: '60%' }} />
    ) : (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <div style={{ color, fontSize: 13, fontWeight: 500, marginBottom: 6, opacity: 0.8 }}>
            {title}
          </div>
          <div style={{ color, fontSize: 32, fontWeight: 700, lineHeight: 1 }}>{value}</div>
        </div>
        <div
          style={{
            width: 48,
            height: 48,
            borderRadius: 12,
            background: `${color}22`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 22,
            color,
          }}
        >
          {icon}
        </div>
      </div>
    )}
  </Card>
);

export default StatCard;
