import { Card, Skeleton } from 'antd';
import type { ReactNode } from 'react';

export interface ListSkeletonProps {
  count?: number;
  card?: boolean;
  children?: ReactNode;
}

export const ListSkeleton = ({ count = 3, card = true }: ListSkeletonProps) => {
  const items = Array.from({ length: count }).map((_, i) => (
    <Skeleton key={i} active paragraph={{ rows: 2 }} style={{ marginBottom: 12 }} />
  ));
  return card ? <Card>{items}</Card> : <>{items}</>;
};

export default ListSkeleton;
