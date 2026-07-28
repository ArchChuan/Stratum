import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

export const useTenantSettings = () => {
  const { user, login, tokenRef } = useAuth();
  const [loading, setLoading] = useState(false);
  const [keyLoading, setKeyLoading] = useState(false);
  const [fetchLoading, setFetchLoading] = useState(true);
  const [maskedKeys, setMaskedKeys] = useState<Record<string, string>>({});
  const [tenantName, setTenantName] = useState('');
  const [isDefault, setIsDefault] = useState(false);

  const role = user?.current_tenant?.role || user?.role;
  const canEditKeys = role === 'owner' || role === 'admin';

  const loadSettings = useCallback(async () => {
    try {
      const settings = await tenantApi.settings();
      setMaskedKeys(settings.llm_api_keys || {});
      setTenantName(settings.tenant_name || '');
      setIsDefault(settings.is_default ?? false);
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '加载设置失败'));
    } finally {
      setFetchLoading(false);
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


  const handleKeySave = async (llm_api_keys: Record<string, string>) => {
    if (Object.keys(llm_api_keys).length === 0) {
      message.warning('请输入至少一个 API Key');
      return;
    }
    setKeyLoading(true);
    try {
      await tenantApi.updateSettings({ settings: { llm_api_keys } });
      message.success('API Key 已保存');
      await loadSettings();
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '保存失败'));
    } finally {
      setKeyLoading(false);
    }
  };

  return {
    user,
    role,
    canEditKeys,
    loading,
    keyLoading,
    fetchLoading,
    maskedKeys,
    tenantName,
    isDefault,
    handleBasicSave,
    handleKeySave,
  };
};
