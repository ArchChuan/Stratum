import { message } from 'antd';
import { useCallback, useState } from 'react';

import {
  operationProposalApi,
  type GrantableResourceType,
  type RequestEditorAccessOptions,
} from '@/modules/operation-gate';
import { extractErrorMessage } from '@/shared/lib';

// 共享「申请权限」入口：封装发起 grant_editor 提案，成功统一提示进入审批中心。
export function useRequestEditorAccess(resourceType: GrantableResourceType, resourceId: string, options?: RequestEditorAccessOptions) {
  const [requesting, setRequesting] = useState(false);
  const request = useCallback(async (): Promise<boolean> => {
    setRequesting(true);
    try {
      await operationProposalApi.requestEditorAccess(resourceType, resourceId, options);
      message.success({ content: '已提交，等待管理员审批', duration: 2 });
      return true;
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '操作失败'), duration: 3 });
      return false;
    } finally {
      setRequesting(false);
    }
  }, [resourceType, resourceId, options]);

  return { requesting, request };
}
