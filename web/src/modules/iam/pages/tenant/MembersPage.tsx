import { UserAddOutlined } from '@ant-design/icons';
import { Button, Typography } from 'antd';

import { TenantInviteModal } from '../../components/TenantInviteModal';
import { TenantMemberTable } from '../../components/TenantMemberTable';
import { useTenantMembers } from '../../hooks/useTenantMembers';

const { Title, Text } = Typography;

export const MembersPage = () => {
  const {
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
  } = useTenantMembers();

  return (
    <div>
      <div
        className="responsive-page-header"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 20,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            成员管理
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            管理团队成员及其权限
          </Text>
        </div>
        {canInvite && (
          <Button type="primary" icon={<UserAddOutlined />} onClick={() => setInviteOpen(true)}>
            邀请成员
          </Button>
        )}
      </div>

      <TenantMemberTable
        members={members}
        loading={loading}
        currentUserSub={user?.sub}
        currentUserRole={user?.role}
        isOwner={isOwner}
        onRemove={handleRemove}
        onRoleChange={handleRoleChange}
      />

      <TenantInviteModal
        open={inviteOpen}
        loading={inviteLoading}
        onCancel={() => setInviteOpen(false)}
        onSubmit={handleInvite}
      />
    </div>
  );
};

export default MembersPage;
