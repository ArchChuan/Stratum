import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

export const useTenantSettings = () => {
  const { user, login, tokenRef } = useAuth();
  const [loading, setLoading] = useState(false);
  const [tenantName, setTenantName] = useState('');
  const [isDefault, setIsDefault] = useState<boolean | null>(null);

  const role = user?.current_tenant?.role || user?.role;
  const loadSettings = useCallback(async () => {
    try {
      const settings = await tenantApi.settings();
      setTenantName(settings.tenant_name || '');
      setIsDefault(settings.is_default ?? false);
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '加载设置失败'));
    }
  }, []);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const handleBasicSave = async (values: { name: string }) => {
    setLoading(true);
    try {
      await tenantApi.updateSettings(values);
      message.success('设置已保存');
      setTenantName(values.name);
      if (user) {
        const currentTenantID = user.current_tenant?.id ?? user.tenant_id ?? '';
        login(
          { ...user, current_tenant: { ...(user.current_tenant ?? {}), id: currentTenantID, ...values } },
          tokenRef.current ?? '',
        );
      }
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '保存失败'));
    } finally {
      setLoading(false);
    }
  };
  return {
    user,
    role,
    loading,
    tenantName,
    isDefault,
    handleBasicSave,
  };
};
