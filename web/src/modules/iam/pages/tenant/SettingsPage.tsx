import { Col, Row, Typography } from 'antd';

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
    handleBasicSave,
    handleEmbedSave,
    handleKeySave,
  } = useTenantSettings();

  return (
    <div style={{ maxWidth: 960 }}>
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
            initialName={user?.current_tenant?.name || ''}
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
    </div>
  );
};

export default SettingsPage;
