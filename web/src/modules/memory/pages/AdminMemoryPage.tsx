import { Select, Space, Typography } from 'antd';
import { useState } from 'react';

import { MemoryDiagnosticsPanel } from '../components/MemoryDiagnosticsPanel';

const { Title } = Typography;
const { Option } = Select;

export const AdminMemoryPage = () => {
  const [selectedTenant, setSelectedTenant] = useState<string>('tenant_default');

  // TODO: Replace with actual tenant list API call
  const tenants = [
    { id: 'tenant_default', name: '系统租户' },
    { id: 'tenant_acme', name: 'Acme Corp' },
  ];

  return (
    <div style={{ padding: 24 }}>
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        <div>
          <Title level={2}>内存诊断</Title>
          <Select
            value={selectedTenant}
            onChange={setSelectedTenant}
            style={{ width: 300 }}
            placeholder="选择租户"
          >
            {tenants.map((t) => (
              <Option key={t.id} value={t.id}>
                {t.name}
              </Option>
            ))}
          </Select>
        </div>

        {selectedTenant && <MemoryDiagnosticsPanel tenantId={selectedTenant} />}
      </Space>
    </div>
  );
};
