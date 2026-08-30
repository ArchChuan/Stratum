import { Button } from 'antd';
import type { ButtonProps } from 'antd';

import { useRequestEditorAccess } from '../hooks/useRequestEditorAccess';

import type { GrantableResourceType, RequestEditorAccessOptions } from '@/modules/operation-gate';

interface RequestEditorButtonProps {
  resourceType: GrantableResourceType;
  resourceId: string;
  options?: RequestEditorAccessOptions;
  buttonProps?: ButtonProps;
}

// 统一申请入口按钮：knowledge_doc 为「申请查看权限」，其余为「申请编辑权限」。
export function RequestEditorButton({ resourceType, resourceId, options, buttonProps }: RequestEditorButtonProps) {
  const { requesting, request } = useRequestEditorAccess(resourceType, resourceId, options);
  const label = resourceType === 'knowledge_doc' ? '申请查看权限' : '申请编辑权限';
  return (
    <Button {...buttonProps} loading={requesting} onClick={() => { void request(); }}>
      {label}
    </Button>
  );
}
