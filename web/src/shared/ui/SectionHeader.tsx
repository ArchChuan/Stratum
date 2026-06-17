import { Typography } from 'antd';
import { cloneElement } from 'react';
import type { ReactElement } from 'react';

const { Text } = Typography;

export interface SectionHeaderProps {
  icon: ReactElement<{ style?: React.CSSProperties }>;
  title: string;
  subtitle?: string;
}

export const SectionHeader = ({ icon, title, subtitle }: SectionHeaderProps) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
    <div
      style={{
        width: 32,
        height: 32,
        borderRadius: 8,
        background: '#f0f5ff',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      {cloneElement(icon, { style: { color: '#2f54eb', fontSize: 16 } })}
    </div>
    <div>
      <Text strong style={{ fontSize: 14, display: 'block' }}>
        {title}
      </Text>
      {subtitle && (
        <Text type="secondary" style={{ fontSize: 12 }}>
          {subtitle}
        </Text>
      )}
    </div>
  </div>
);
