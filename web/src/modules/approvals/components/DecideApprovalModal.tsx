import { Input, Modal } from 'antd';

import type { ApprovalDecision } from '../api';

export interface DecideTarget {
  id: string;
  decision: ApprovalDecision;
}

interface DecideApprovalModalProps {
  target: DecideTarget | null;
  reason: string;
  confirmLoading: boolean;
  onReasonChange: (value: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
}

export const DecideApprovalModal = ({
  target,
  reason,
  confirmLoading,
  onReasonChange,
  onConfirm,
  onCancel,
}: DecideApprovalModalProps) => {
  const isApprove = target?.decision === 'approved';
  return (
    <Modal
      title={isApprove ? '批准审批' : '拒绝审批'}
      open={target !== null}
      okText={isApprove ? '批准' : '拒绝'}
      okButtonProps={{ danger: !isApprove }}
      confirmLoading={confirmLoading}
      onOk={onConfirm}
      onCancel={onCancel}
    >
      <Input.TextArea
        rows={3}
        value={reason}
        onChange={(e) => onReasonChange(e.target.value)}
        placeholder="处理原因（可选）"
      />
    </Modal>
  );
};

export default DecideApprovalModal;
