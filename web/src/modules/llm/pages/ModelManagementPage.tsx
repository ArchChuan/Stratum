import { Tabs, Typography } from 'antd';
import { ProviderListPage } from './ProviderListPage';
import { ModelListPage } from './ModelListPage';

const { Title } = Typography;

export function ModelManagementPage() {
  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 20 }}>
        模型管理
      </Title>
      <Tabs
        defaultActiveKey="providers"
        items={[
          { key: 'providers', label: '厂商管理', children: <ProviderListPage /> },
          { key: 'models', label: '模型目录', children: <ModelListPage /> },
        ]}
      />
    </div>
  );
}
