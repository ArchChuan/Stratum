import { UploadOutlined, UserOutlined } from '@ant-design/icons';
import { Avatar, Button, Card, Space, Typography, Upload, message } from 'antd';
import type { RcFile, UploadChangeParam } from 'antd/es/upload';
import { useState } from 'react';

import { authApi } from '../api/auth.api';
import { useAuth } from '../components/AuthContext';

import { AVATAR_MAX_UPLOAD_SIZE_BYTES } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

const ACCEPTED_AVATAR_EXTENSIONS = ['jpg', 'jpeg', 'png', 'webp'];

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
              style={{ background: '#2563eb', marginBottom: 12 }}
            />
            <br />
            {isPasswordUser && (
              <>
                <Upload
                  accept="image/jpeg,image/png,image/webp"
                  showUploadList={false}
                  beforeUpload={(file) => {
                    const ext = file.name.split('.').pop()?.toLowerCase() ?? '';
                    if (!ACCEPTED_AVATAR_EXTENSIONS.includes(ext)) {
                      message.error({ content: '头像仅支持 JPG / PNG / WebP', duration: 0 });
                      return Upload.LIST_IGNORE;
                    }
                    if (file.size > AVATAR_MAX_UPLOAD_SIZE_BYTES) {
                      message.error({ content: '头像不能超过 2MB', duration: 0 });
                      return Upload.LIST_IGNORE;
                    }
                    return false;
                  }}
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
