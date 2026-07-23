import { Divider, Flex, Typography } from 'antd';

import type { CenterOverview } from '../model/evaluation';

export const EvaluationOverview = ({ overview }: { overview: CenterOverview | null }) => {
  const entries = [
    ['资源', overview?.resources ?? 0], ['评测集', overview?.suites ?? 0], ['运行', overview?.runs ?? 0],
    ['候选', overview?.candidates ?? 0], ['实验', overview?.experiments ?? 0],
  ];
  return (
    <Flex align="center" gap={12} wrap style={{ padding: '10px 0' }}>
      <Typography.Text strong>实验记录簿</Typography.Text>
      <Divider type="vertical" />
      {entries.map(([label, value]) => <Typography.Text type="secondary" key={label}>{label} {value}</Typography.Text>)}
    </Flex>
  );
};
