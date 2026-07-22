import { Alert, Button, Collapse, Drawer, Flex, Modal, Typography } from 'antd';

import type { CandidateSummary } from '../model/evaluation';

import { drawerWidth, SafeValue, StatusTag } from './evaluationView';

export const CandidateDrawer = ({ candidate, open, onClose, canManage, onReject, isMobile }: {
  candidate: CandidateSummary | null; open: boolean; onClose: () => void; canManage: boolean;
  onReject: (candidate: CandidateSummary) => void; isMobile?: boolean;
}) => {
  const reject = () => candidate && Modal.confirm({ title: '确认拒绝此候选版本？',
    content: '拒绝后该候选不会进入金丝雀实验，决定会记录在评测时间线中。', okText: '拒绝候选', cancelText: '取消',
    okButtonProps: { danger: true }, onOk: () => onReject(candidate) });
  return <Drawer title="候选版本" open={open} onClose={onClose} width={drawerWidth(isMobile)} destroyOnHidden
    extra={canManage && candidate ? <Button danger onClick={reject}>拒绝候选</Button> : null}>
    {candidate && <>
      <Typography.Title level={5}>观测事实</Typography.Title>
      <Flex gap={8}><StatusTag value={candidate.status} /><Typography.Text type="secondary">来源：{candidate.source}</Typography.Text></Flex>
      {candidate.safe_diff.parent_missing && <Alert type="warning" message="父版本已不可用，仅展示可验证的变更字段。" />}
      <Collapse defaultActiveKey={candidate.safe_diff.changed_fields} style={{ marginTop: 16 }} items={candidate.safe_diff.changed_fields.map((field) => ({ key: field, label: field,
        children: <Flex vertical={isMobile} gap={12}><div><Typography.Text type="secondary">变更前</Typography.Text><br />
          <SafeValue value={candidate.safe_diff.changes[field]?.before} /></div><div><Typography.Text type="secondary">变更后</Typography.Text><br />
          <SafeValue value={candidate.safe_diff.changes[field]?.after} /></div></Flex> }))} />
      {canManage && <><Typography.Title level={5}>管理员决定</Typography.Title>
        <Typography.Paragraph type="secondary">候选决定只基于以上安全差异与评测证据。</Typography.Paragraph></>}
    </>}
  </Drawer>;
};
