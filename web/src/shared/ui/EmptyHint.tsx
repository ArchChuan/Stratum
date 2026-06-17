import { Empty, Button } from 'antd';
import type { ReactNode } from 'react';

export interface EmptyHintProps {
  title?: string;
  description?: string;
  actionLabel?: string;
  onAction?: () => void;
  image?: ReactNode;
}

export const EmptyHint = ({ title, description, actionLabel, onAction, image }: EmptyHintProps) => (
  <Empty
    image={image ?? Empty.PRESENTED_IMAGE_SIMPLE}
    description={
      <div>
        {title && <div style={{ fontSize: 14, marginBottom: 4 }}>{title}</div>}
        {description && (
          <div style={{ fontSize: 12, color: 'rgba(0,0,0,0.45)' }}>{description}</div>
        )}
      </div>
    }
  >
    {actionLabel && onAction && (
      <Button type="primary" onClick={onAction}>
        {actionLabel}
      </Button>
    )}
  </Empty>
);

export default EmptyHint;
