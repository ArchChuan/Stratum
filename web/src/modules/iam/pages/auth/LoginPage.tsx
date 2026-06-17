import { GithubOutlined } from '@ant-design/icons';
import { Button, Card, Typography, Space } from 'antd';

const { Title, Text } = Typography;

const handleGithubLogin = () => {
  window.location.href = '/auth/github';
};

export const LoginPage = () => (
  <div
    style={{
      minHeight: '100vh',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      background: '#f0f2f5',
    }}
  >
    <Card
      style={{ width: 380, textAlign: 'center', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
    >
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        <div>
          <Title level={2} style={{ marginBottom: 4 }}>
            Stratum AI
          </Title>
          <Text type="secondary">多租户 AI Agent 平台</Text>
        </div>
        <Button
          type="primary"
          size="large"
          icon={<GithubOutlined />}
          block
          onClick={handleGithubLogin}
        >
          使用 GitHub 登录
        </Button>
        <Text type="secondary" style={{ fontSize: 12 }}>
          登录即代表同意服务条款
        </Text>
      </Space>
    </Card>
  </div>
);

export default LoginPage;
