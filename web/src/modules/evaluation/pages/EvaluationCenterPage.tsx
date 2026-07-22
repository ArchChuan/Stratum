import { Alert, Card, Skeleton, Typography } from 'antd';

import { useEvaluationCenter } from '../hooks/useEvaluationCenter';

export const EvaluationCenterPage = () => {
  const { overview, loading, error } = useEvaluationCenter();
  if (loading) return <Skeleton active />;
  if (error) return <Alert type="error" showIcon message={error} />;
  return (
    <Card>
      <Typography.Title level={2}>评测与进化</Typography.Title>
      <Typography.Text>已接入 {overview?.resources ?? 0} 个评测资源</Typography.Text>
    </Card>
  );
};
