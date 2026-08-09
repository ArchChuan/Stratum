import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

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
    } catch (err: unknown) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '加载设置失败'), duration: 0 });
      }
    }
  }, []);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const handleBasicSave = async (values: { name: string }) => {
    setLoading(true);
    try {
      await tenantApi.updateSettings(values);
      message.success({ content: '设置已保存', duration: 2 });
      setTenantName(values.name);
      if (user) {
        const currentTenantID = user.current_tenant?.id ?? user.tenant_id ?? '';
        login(
          { ...user, current_tenant: { ...(user.current_tenant ?? {}), id: currentTenantID, ...values } },
          tokenRef.current ?? '',
        );
      }
    } catch (err: unknown) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '保存失败'), duration: 0 });
      }
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
