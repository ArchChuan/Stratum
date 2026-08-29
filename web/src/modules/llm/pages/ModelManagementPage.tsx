import { Tabs, Typography } from 'antd';
import { useState } from 'react';

import { ModelListPage } from './ModelListPage';
import { ProviderListPage } from './ProviderListPage';

const { Title } = Typography;

export function ModelManagementPage() {
  // 厂商管理手动添加模型后递增，通知模型目录页刷新。
  const [modelTick, setModelTick] = useState(0);

  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 20 }}>
        模型管理
      </Title>
      <Tabs
        defaultActiveKey="providers"
        items={[
          {
            key: 'providers',
            label: '厂商管理',
            children: <ProviderListPage onModelCreated={() => setModelTick((t) => t + 1)} />,
          },
          { key: 'models', label: '模型目录', children: <ModelListPage refreshTick={modelTick} /> },
        ]}
      />
    </div>
  );
}
