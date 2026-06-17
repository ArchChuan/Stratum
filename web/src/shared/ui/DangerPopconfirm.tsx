import { DeleteOutlined } from '@ant-design/icons';
import { Button, Popconfirm } from 'antd';
import type { ReactNode } from 'react';

export interface DangerPopconfirmProps {
  title?: ReactNode;
  description?: ReactNode;
  okText?: string;
  cancelText?: string;
  onConfirm: () => void | Promise<void>;
  loading?: boolean;
  disabled?: boolean;
  children?: ReactNode;
}

export const DangerPopconfirm = ({
  title = '确认删除？',
  description,
  okText = '删除',
  cancelText = '取消',
  onConfirm,
  loading,
  disabled,
  children,
}: DangerPopconfirmProps) => (
  <Popconfirm
    title={title}
    description={description}
    okText={okText}
    cancelText={cancelText}
    okButtonProps={{ danger: true, loading }}
    onConfirm={onConfirm}
    disabled={disabled}
  >
    {children ?? (
      <Button danger size="small" icon={<DeleteOutlined />} disabled={disabled}>
        {okText}
      </Button>
    )}
  </Popconfirm>
);

export default DangerPopconfirm;
