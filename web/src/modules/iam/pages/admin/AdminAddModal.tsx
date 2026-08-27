import { SearchOutlined } from '@ant-design/icons';
import { AutoComplete, Avatar, Flex, Input, Modal, Tag, Typography } from 'antd';
import type { ReactNode } from 'react';
import { useEffect, useRef, useState } from 'react';

import { tenantApi, type AdminUser } from '../../api/tenant.api';

const { Text } = Typography;

/** 平台管理员候选用户（来自 GET /admin/users）。 */
export type AdminCandidate = AdminUser;

interface AdminAddModalProps {
  open: boolean;
  onCancel: () => void;
  onAdd: (candidate: AdminCandidate) => void;
}

const SEARCH_DEBOUNCE_MS = 300;

/**
 * 通过 AutoComplete 按用户名/GitHub 登录名搜索用户并选择添加为平台管理员。
 * 超级管理员（global_admin）不可经 UI 产生，候选直接禁用，后端 RequireGlobalAdmin
 * 语义兜底。
 */
export const AdminAddModal = ({ open, onCancel, onAdd }: AdminAddModalProps) => {
  const [options, setOptions] = useState<{ value: string; label: ReactNode; disabled?: boolean }[]>([]);
  const timerRef = useRef<number | undefined>(undefined);
  const candidateRef = useRef<Map<string, AdminCandidate>>(new Map());

  useEffect(
    () => () => {
      window.clearTimeout(timerRef.current);
    },
    [],
  );

  const handleSearch = (query: string) => {
    window.clearTimeout(timerRef.current);
    if (!query.trim()) {
      setOptions([]);
      return;
    }
    timerRef.current = window.setTimeout(async () => {
      try {
        const users = await tenantApi.searchAdminCandidates(query.trim());
        candidateRef.current = new Map(users.map((u) => [u.user_id, u]));
        setOptions(
          users.map((u) => ({
            value: u.user_id,
            disabled: u.global_role === 'global_admin',
            label: (
              <Flex align="center" gap={8}>
                <Avatar size="small" src={u.avatar_url}>
                  {u.username?.[0] ?? '?'}
                </Avatar>
                <span>{u.username || u.github_login || u.user_id}</span>
                {u.github_login ? (
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {u.github_login}
                  </Text>
                ) : null}
                {u.global_role === 'global_admin' ? (
                  <Tag color="gold" style={{ marginInlineEnd: 0 }}>
                    超级管理员
                  </Tag>
                ) : null}
              </Flex>
            ),
          })),
        );
      } catch {
        setOptions([]);
      }
    }, SEARCH_DEBOUNCE_MS);
  };

  const handleSelect = (userID: string) => {
    const candidate = candidateRef.current.get(userID);
    if (candidate && candidate.global_role !== 'global_admin') {
      onAdd(candidate);
    }
  };

  const handleCancel = () => {
    window.clearTimeout(timerRef.current);
    candidateRef.current.clear();
    setOptions([]);
    onCancel();
  };

  return (
    <Modal title="添加平台管理员" open={open} onCancel={handleCancel} footer={null} destroyOnHidden>
      <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>
        输入用户名或 GitHub 登录名搜索用户，选择后即添加为平台管理员。
      </Text>
      <AutoComplete style={{ width: '100%' }} options={options} onSearch={handleSearch} onSelect={handleSelect}>
        <Input prefix={<SearchOutlined />} aria-label="搜索用户" placeholder="搜索用户..." allowClear />
      </AutoComplete>
    </Modal>
  );
};

export default AdminAddModal;
