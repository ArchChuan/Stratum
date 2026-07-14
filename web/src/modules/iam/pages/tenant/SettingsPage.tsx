import { Col, Modal, Row, Typography, Button, message } from 'antd';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { tenantApi } from '../../api/tenant.api';
import { TenantApiKeyCard } from '../../components/TenantApiKeyCard';
import { TenantBasicCard } from '../../components/TenantBasicCard';
import { TenantEmbeddingCard } from '../../components/TenantEmbeddingCard';
import { useTenantSettings } from '../../hooks/useTenantSettings';

const { Title, Text } = Typography;

export const SettingsPage = () => {
  const {
    user,
    role,
    canEditKeys,
    loading,
    keyLoading,
    fetchLoading,
    maskedKeys,
    embedModel,
    embedLoading,
    tenantName,
    isDefault,
    handleBasicSave,
    handleEmbedSave,
    handleKeySave,
  } = useTenantSettings();

  const navigate = useNavigate();
  const [deleteLoading, setDeleteLoading] = useState(false);

  const handleDeleteTenant = () => {
    Modal.confirm({
      className: 'mobile-overlay',
      title: '删除租户',
      content: `确定要删除租户「${tenantName || user?.current_tenant?.name || ''}」吗？此操作不可恢复，租户下的所有数据（成员、智能体、知识库、记忆）将被永久清除。`,
      okText: '确认删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        setDeleteLoading(true);
        try {
          await tenantApi.deleteSelf();
          message.success('租户已删除');
          navigate('/');
        } catch (err: any) {
          message.error(err?.response?.data?.error || '删除失败');
        } finally {
          setDeleteLoading(false);
        }
      },
    });
  };

  return (
    <div style={{ width: '100%', maxWidth: 960 }}>
      <div style={{ marginBottom: 24 }}>
        <Title level={4} style={{ margin: 0 }}>
          租户设置
        </Title>
        <Text type="secondary" style={{ fontSize: 13 }}>
          管理租户基本信息与 LLM 接口配置
        </Text>
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={24} md={12} xl={10}>
          <TenantBasicCard
            initialName={tenantName || user?.current_tenant?.name || ''}
            loading={loading}
            onSave={handleBasicSave}
          />
        </Col>
        <Col xs={24} md={12} xl={14}>
          <TenantEmbeddingCard
            embedModel={embedModel}
            fetchLoading={fetchLoading}
            embedLoading={embedLoading}
            canEditKeys={canEditKeys}
            role={role}
            onSave={handleEmbedSave}
          />
        </Col>
      </Row>

      <TenantApiKeyCard
        maskedKeys={maskedKeys}
        fetchLoading={fetchLoading}
        keyLoading={keyLoading}
        canEditKeys={canEditKeys}
        role={role}
        onSave={handleKeySave}
      />

      {role === 'owner' && !isDefault && (
        <div
          style={{
            marginTop: 24,
            padding: 16,
            border: '1px solid #ff4d4f',
            borderRadius: 8,
          }}
        >
          <Title level={5} style={{ color: '#ff4d4f', margin: '0 0 8px' }}>
            危险操作
          </Title>
          <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
            删除租户将永久清除所有数据，包括成员、智能体、知识库和记忆。此操作不可撤销。
          </Text>
          <Button danger loading={deleteLoading} onClick={handleDeleteTenant}>
            删除租户
          </Button>
        </div>
      )}
    </div>
  );
};

export default SettingsPage;
