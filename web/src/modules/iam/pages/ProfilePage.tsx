import { UploadOutlined, UserOutlined } from '@ant-design/icons';
import { Avatar, Button, Card, message, Space, Typography, Upload } from 'antd';
import type { RcFile, UploadChangeParam } from 'antd/es/upload';
import { useState } from 'react';

import { authApi } from '../api/auth.api';
import { useAuth } from '../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

export const ProfilePage = () => {
  const { user, login } = useAuth();
  const [uploading, setUploading] = useState(false);

  if (!user) return null;

  // Password users get a username; GitHub users get github_login from OAuth.
  const isPasswordUser = !!user.username;
  const accountName = user.username || user.github_login || '';

  const handleAvatarChange = async (info: UploadChangeParam) => {
    const file = (info.file.originFileObj || info.file) as RcFile | undefined;
    if (!file) return;
    setUploading(true);
    try {
      const { avatar_url } = await authApi.uploadAvatar(file);
      const { access_token: newToken } = await authApi.refresh();
      login({ ...user, avatar_url }, newToken);
      message.success({ content: '头像已更新', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '上传失败'), duration: 0 });
    } finally {
      setUploading(false);
    }
  };

  return (
    <div style={{ maxWidth: 480, margin: '0 auto', padding: '24px 0' }}>
      <Card>
        <Title level={4} style={{ marginBottom: 24 }}>
          个人设置
        </Title>

        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <div style={{ textAlign: 'center' }}>
            <Avatar
              src={user.avatar_url}
              icon={<UserOutlined />}
              size={80}
              style={{ background: '#1677ff', marginBottom: 12 }}
            />
            <br />
            {isPasswordUser && (
              <>
                <Upload
                  accept="image/jpeg,image/png,image/webp"
                  showUploadList={false}
                  beforeUpload={() => false}
                  onChange={handleAvatarChange}
                  disabled={uploading}
                >
                  <Button icon={<UploadOutlined />} size="small" loading={uploading}>
                    更换头像
                  </Button>
                </Upload>
                <div style={{ fontSize: 12, color: '#999', marginTop: 4 }}>
                  支持 JPG / PNG / WebP，不超过 2MB
                </div>
              </>
            )}
          </div>

          <div>
            <Text type="secondary">账号名</Text>
            <div>
              <Text strong>{accountName}</Text>
            </div>
          </div>
        </Space>
      </Card>
    </div>
  );
};

export default ProfilePage;
