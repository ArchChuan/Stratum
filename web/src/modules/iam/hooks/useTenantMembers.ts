import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';
import type { Member } from '../model/auth';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

interface PaginationState {
  current: number;
  pageSize: number;
  total: number;
}

export const useTenantMembers = () => {
  const { user } = useAuth();
  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteLoading, setInviteLoading] = useState(false);
  const [invitationCode, setInvitationCode] = useState('');
  const [pagination, setPagination] = useState<PaginationState>({
    current: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    total: 0,
  });

  const isOwner = user?.role === 'owner';
  const canInvite = user?.role === 'owner' || user?.role === 'admin';

  const openInvite = () => {
    setInvitationCode('');
    setInviteOpen(true);
  };

  const closeInvite = () => {
    setInviteOpen(false);
    setInvitationCode('');
  };

  const fetchPage = useCallback(async (page: number, pageSize: number) => {
    setLoading(true);
    try {
      const data = await tenantApi.members(page, pageSize);
      setMembers(data.members);
      setPagination({ current: data.page, pageSize: data.page_size, total: data.total });
      return data;
    } catch {
      message.error('获取成员列表失败');
      return undefined;
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchPage(1, DEFAULT_PAGE_SIZE);
  }, [fetchPage]);

  const handleInvite = async (values: { email: string; role: 'admin' | 'member' }) => {
    setInviteLoading(true);
    try {
      const invitation = await tenantApi.inviteMember(values);
      setInvitationCode(invitation.invitation_code);
      message.success({ content: '邀请码已生成', duration: 2 });
    } catch (err: any) {
      if (err?.response?.status !== 403) {
        message.error({ content: extractErrorMessage(err, '邀请失败'), duration: 0 });
      }
    } finally {
      setInviteLoading(false);
    }
  };

  const handleRemove = async (userId: string) => {
    try {
      await tenantApi.removeMember(userId);
      message.success('成员已移除');
      const data = await fetchPage(pagination.current, pagination.pageSize);
      if (data && data.members.length === 0 && data.page > 1 && data.total > 0) {
        await fetchPage(data.page - 1, data.page_size);
      }
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '移除失败'));
    }
  };

  const handleRoleChange = async (userId: string, role: string) => {
    try {
      await tenantApi.updateMemberRole(userId, role);
      message.success('角色已更新');
      void fetchPage(pagination.current, pagination.pageSize);
    } catch (err: any) {
      if (err?.response?.status !== 403) message.error(extractErrorMessage(err, '更新角色失败'));
    }
  };

  return {
    user,
    members,
    loading,
    pagination,
    fetchPage,
    inviteOpen,
    openInvite,
    closeInvite,
    inviteLoading,
    invitationCode,
    isOwner,
    canInvite,
    handleInvite,
    handleRemove,
    handleRoleChange,
  };
};
