import { UploadOutlined, UserOutlined } from '@ant-design/icons';
import { Avatar, Button, Card, Form, Input, message, Space, Typography, Upload } from 'antd';
import type { RcFile, UploadChangeParam } from 'antd/es/upload';
import { useState } from 'react';

import { authApi } from '../api/auth.api';
import { useAuth } from '../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title } = Typography;

export const ProfilePage = () => {
  const { user, login } = useAuth();
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [form] = Form.useForm();

  if (!user) return null;

  const handleSaveName = async (values: { display_name: string }) => {
    setSaving(true);
    try {
      await authApi.updateProfile(values.display_name);
      // Refresh token to embed updated claims, then update context.
      const { access_token: newToken } = await authApi.refresh();
      login({ ...user, github_login: values.display_name }, newToken);
      message.success({ content: '昵称已更新', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '保存失败'), duration: 0 });
    } finally {
      setSaving(false);
    }
  };

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
          </div>

          <Form
            form={form}
            layout="vertical"
            initialValues={{ display_name: user.github_login }}
            onFinish={handleSaveName}
          >
            <Form.Item
              label="昵称"
              name="display_name"
              rules={[{ required: true, message: '请输入昵称' }]}
            >
              <Input maxLength={64} placeholder="输入新昵称" />
            </Form.Item>
            <Form.Item label="账号">
              <Input value={user.sub} disabled />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={saving} block>
                保存
              </Button>
            </Form.Item>
          </Form>
        </Space>
      </Card>
    </div>
  );
};

export default ProfilePage;
