import { Alert, Descriptions, Drawer, Progress, Typography } from 'antd';

import type { RunSummary } from '../model/evaluation';

import { drawerWidth, StatusTag } from './evaluationView';

export const RunDrawer = ({ run, open, onClose, isMobile }: {
  run: RunSummary | null; open: boolean; onClose: () => void; isMobile?: boolean;
}) => (
  <Drawer title="评测运行" open={open} onClose={onClose} width={drawerWidth(isMobile)} destroyOnHidden>
    {run && <>
      <Typography.Title level={5}>观测事实</Typography.Title>
      <Descriptions bordered size="small" column={isMobile ? 1 : 2}>
        <Descriptions.Item label="运行状态"><StatusTag value={run.status} /></Descriptions.Item>
        <Descriptions.Item label="资源版本">{run.revision_id}</Descriptions.Item>
        <Descriptions.Item label="通过用例">{run.passed_cases} / {run.total_cases}</Descriptions.Item>
        <Descriptions.Item label="创建时间">{new Date(run.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
      </Descriptions>
      <Progress percent={run.total_cases ? Math.round(run.passed_cases / run.total_cases * 100) : 0} />
      {!run.passed && <Alert type="warning" showIcon message="运行未通过，请依据已脱敏的失败摘要定位问题。" />}
    </>}
  </Drawer>
);
