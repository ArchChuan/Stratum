import { message } from 'antd';
import { useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';
import type { Member } from '../model/auth';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

export const useTenantMembers = () => {
  const { user } = useAuth();
  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteLoading, setInviteLoading] = useState(false);

  const isOwner = user?.role === 'owner';
  const canInvite = user?.role === 'owner' || user?.role === 'admin';

  const fetchMembers = async () => {
    setLoading(true);
    try {
      setMembers(await tenantApi.members());
    } catch {
      message.error('获取成员列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchMembers();
  }, []);

  const handleInvite = async (values: { email: string; role: 'admin' | 'member' }) => {
    setInviteLoading(true);
    try {
      await tenantApi.inviteMember(values);
      message.success('邀请已发送');
      setInviteOpen(false);
      fetchMembers();
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '邀请失败'));
    } finally {
      setInviteLoading(false);
    }
  };

  const handleRemove = async (userId: string) => {
    try {
      await tenantApi.removeMember(userId);
      message.success('成员已移除');
      fetchMembers();
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '移除失败'));
    }
  };

  const handleRoleChange = async (userId: string, role: string) => {
    try {
      await tenantApi.updateMemberRole(userId, role);
      message.success('角色已更新');
      fetchMembers();
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '更新角色失败'));
    }
  };

  return {
    user,
    members,
    loading,
    inviteOpen,
    setInviteOpen,
    inviteLoading,
    isOwner,
    canInvite,
    handleInvite,
    handleRemove,
    handleRoleChange,
  };
};
