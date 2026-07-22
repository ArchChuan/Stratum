import { Alert, Button, Descriptions, Drawer, Flex, Modal, Typography } from 'antd';

import type { ExperimentSummary } from '../model/evaluation';

import { drawerWidth, StatusTag } from './evaluationView';
import { experimentActions, promotionBlockReason } from './experimentDecisions';

const gateLabels: Record<string, string> = { quality: '质量', cost: '成本', latency: '时延', error_rate: '错误率', security: '安全' };

export const ExperimentDrawer = ({ experiment, open, onClose, canManage, onPause, onPromote, onRollback, isMobile }: {
  experiment: ExperimentSummary | null; open: boolean; onClose: () => void; canManage: boolean; isMobile?: boolean;
  onPause: (value: ExperimentSummary) => void; onPromote: (value: ExperimentSummary) => void;
  onRollback: (value: ExperimentSummary) => void;
}) => {
  const block = experiment ? promotionBlockReason(experiment) : '';
  const actions = experiment ? experimentActions(experiment.status) : [];
  const confirm = (kind: '暂停' | '晋级' | '回滚', callback: (value: ExperimentSummary) => void) => experiment && Modal.confirm({
    title: kind === '暂停' ? '确认暂停金丝雀实验？' : `确认${kind}此实验？`,
    content: kind === '暂停' ? '暂停后新流量不再进入候选版本，现有观测事实会保留。'
      : kind === '晋级' ? '晋级会扩大候选版本流量，请确认所有门禁证据有效。' : '回滚会立即把流量切回稳定版本。',
    okText: kind, cancelText: '取消', okButtonProps: { danger: kind !== '晋级' }, onOk: () => callback(experiment),
  });
  const footer = canManage && actions.length && experiment ? <Flex gap={8} justify="end" wrap>
    {actions.includes('pause') && <Button onClick={() => confirm('暂停', onPause)}>暂停实验</Button>}
    {actions.includes('promote') && <Button type="primary" disabled={!!block} title={block}
      onClick={() => confirm('晋级', onPromote)}>晋级</Button>}
    {actions.includes('rollback') && <Button danger onClick={() => confirm('回滚', onRollback)}>回滚</Button>}
  </Flex> : undefined;
  return <Drawer title="金丝雀实验" open={open} onClose={onClose} width={drawerWidth(isMobile)} footer={footer} destroyOnHidden>
    {experiment && <>
      <Typography.Title level={5}>观测事实</Typography.Title>
      <Descriptions bordered size="small" column={isMobile ? 1 : 2}>
        <Descriptions.Item label="状态"><StatusTag value={experiment.status} /></Descriptions.Item>
        <Descriptions.Item label="当前流量">{experiment.stage_percent}%</Descriptions.Item>
        <Descriptions.Item label="稳定版本">{experiment.stable_revision_id}</Descriptions.Item>
        <Descriptions.Item label="候选版本">{experiment.canary_revision_id}</Descriptions.Item>
      </Descriptions>
      <Typography.Title level={5}>已配置门禁</Typography.Title>
      <Flex gap={8} wrap>{Object.entries(experiment.promotion_evidence.gates).map(([key, value]) =>
        <span key={key}>{gateLabels[key]} <StatusTag value={value} /></span>)}</Flex>
      <Typography.Title level={5}>系统建议</Typography.Title>
      <Alert type={block ? 'warning' : 'info'} showIcon message={experiment.recommendation || '继续收集观测证据'}
        description={block || '全部门禁已通过，系统建议晋级。'} />
      {canManage && actions.length > 0 && <><Typography.Title level={5}>管理员决定</Typography.Title>
        <Typography.Text type="secondary">可用决定固定在抽屉底部，并随实验状态变化。</Typography.Text></>}
    </>}
  </Drawer>;
};
